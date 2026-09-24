package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/branow/dbmap/llm"
)

// Batches are sized by prompt characters, then by count.
const (
	BatchChars = 40000
	BatchMax   = 12
)

// DefaultWorkers is one: a library caller gets deterministic ordering unless it
// asks for otherwise. The command sets this to its own concurrency ceiling,
// because describing is the long pole of a build and every batch is
// independent.
const DefaultWorkers = 1

// The field is `sentence`, never `description`, and its text is an ORDER, not
// a noun phrase: models fill a `description` field with
// a description of the field instead. A test pins both.
const sentenceField = "Put the finished sentence here verbatim. Never describe what the sentence would say."

// Schema is the structured output one batch must return. Answers carry the
// object name because they are matched by name, never by position.
var Schema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["objects"],
  "properties": {
    "objects": {
      "type": "array",
      "description": "One entry per object described.",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name", "sentence"],
        "properties": {
          "name": {
            "type": "string",
            "description": "The object name exactly as it appeared in its header."
          },
          "sentence": {
            "type": "string",
            "description": "` + sentenceField + `"
          }
        }
      }
    }
  }
}`)

type answer struct {
	Name     string `json:"name"`
	Sentence string `json:"sentence"`
}

type reply struct {
	Objects []answer `json:"objects"`
}

// Result is what a run produced.
type Result struct {
	// Sentences is keyed by the REQUESTED object key, never by the string the
	// model echoed back: an answer that names nothing asked for is dropped.
	Sentences map[string]string
	// Missing lists objects the model skipped, reported rather than left blank.
	Missing []string
	// Failed lists batches that errored; a failed batch is skipped, never fatal.
	Failed []error
	Usage  llm.Usage
}

// Logger reports batch progress and what was skipped.
type Logger interface {
	Info(message string)
	Warn(message string)
}

// Options configure one describe run.
type Options struct {
	Model string
	Chars int
	Max   int
	// Workers is how many batches may be in flight. 0 means DefaultWorkers.
	Workers int
	Logger  Logger
}

func (o Options) chars() int {
	if o.Chars <= 0 {
		return BatchChars
	}
	return o.Chars
}

func (o Options) max() int {
	if o.Max <= 0 {
		return BatchMax
	}
	return o.Max
}

func (o Options) workers() int {
	if o.Workers <= 0 {
		return DefaultWorkers
	}
	return o.Workers
}

// Item is one object with its prompt already rendered.
type Item struct {
	Key    string
	Prompt string
}

// Prepare renders a prompt per input, dropping kinds that are never described.
func Prepare(inputs []Input) ([]Item, error) {
	items := make([]Item, 0, len(inputs))
	for _, in := range inputs {
		if !Describable(in.Entry.Object.Kind) {
			continue
		}
		prompt, err := Build(in)
		if err != nil {
			return nil, err
		}
		items = append(items, Item{Key: in.Key(), Prompt: prompt})
	}
	return items, nil
}

// Batch groups items by cost. An item over the budget goes alone rather than
// being dropped.
func Batch(items []Item, chars, max int) [][]Item {
	var batches [][]Item
	var current []Item
	spent := 0

	for _, item := range items {
		cost := len(item.Prompt)
		if len(current) > 0 && (spent+cost > chars || len(current) >= max) {
			batches = append(batches, current)
			current, spent = nil, 0
		}
		current = append(current, item)
		spent += cost
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// BatchPrompt renders one batch into a single call.
func BatchPrompt(items []Item) string {
	blocks := make([]string, 0, len(items)+3)
	blocks = append(blocks,
		fmt.Sprintf("Describe %d database objects. Each is delimited below.", len(items)),
		"Return one entry per object, with `name` exactly as given in its header.",
		"")
	for i, item := range items {
		blocks = append(blocks, fmt.Sprintf("=== OBJECT %d: %s ===\n%s", i+1, item.Key, item.Prompt))
	}
	return strings.Join(blocks, "\n\n")
}

// All describes every input it can, in batches. A failing batch is reported on
// Result rather than aborting the run. Batches run opts.workers() at a time and
// are merged back in batch order, so the result does not depend on which
// finished first.
func All(ctx context.Context, client llm.Client, inputs []Input, opts Options) (Result, error) {
	result := Result{Sentences: map[string]string{}}

	items, err := Prepare(inputs)
	if err != nil {
		return result, err
	}
	batches := Batch(items, opts.chars(), opts.max())
	info(opts.Logger, fmt.Sprintf("describe: %s in %s, %s at a time",
		plural(len(items), "object"), plural(len(batches), "batch"),
		plural(opts.workers(), "batch")))

	outcomes := make([]outcome, len(batches))
	run(len(batches), opts.workers(), func(i int) {
		outcomes[i] = describeBatch(ctx, client, batches[i], i, len(batches), opts)
	})

	for _, got := range outcomes {
		if got.err != nil {
			result.Failed = append(result.Failed, got.err)
			continue
		}
		result.Usage.Add(got.usage)
		for key, sentence := range got.sentences {
			result.Sentences[key] = sentence
		}
	}

	for _, item := range items {
		if _, ok := result.Sentences[item.Key]; !ok {
			result.Missing = append(result.Missing, item.Key)
		}
	}
	if len(result.Missing) > 0 {
		warn(opts.Logger, "describe: no sentence for "+strings.Join(result.Missing, ", "))
	}
	return result, nil
}

// outcome is one batch's contribution, held aside until every batch is in so
// the merge happens in batch order rather than completion order.
type outcome struct {
	sentences map[string]string
	usage     llm.Usage
	err       error
}

// describeBatch sends one batch and resolves its answers back onto the keys
// that were asked for.
func describeBatch(ctx context.Context, client llm.Client, batch []Item,
	i, of int, opts Options) outcome {
	response, err := client.Complete(ctx, llm.Request{
		Prompt: BatchPrompt(batch),
		Schema: Schema,
		Model:  opts.Model,
	})
	if err != nil {
		warn(opts.Logger, fmt.Sprintf("describe: batch %d/%d failed: %v", i+1, of, err))
		return outcome{err: err}
	}

	var parsed reply
	if err := json.Unmarshal(response.Structured, &parsed); err != nil {
		warn(opts.Logger,
			fmt.Sprintf("describe: batch %d/%d returned unreadable output: %v", i+1, of, err))
		return outcome{err: err}
	}

	got := outcome{sentences: map[string]string{}, usage: response.Usage}
	asked := requested(batch)
	info(opts.Logger, fmt.Sprintf("describe: batch %d/%d answered (%d objects)",
		i+1, of, len(batch)))
	for _, a := range parsed.Objects {
		sentence := strings.TrimSpace(a.Sentence)
		if sentence == "" {
			continue
		}
		key, ok := resolve(asked, a.Name)
		if !ok {
			// Filing the sentence under the name the model wrote would count it
			// as described while the object it was asked about reports missing.
			warn(opts.Logger, fmt.Sprintf("describe: batch %d/%d answered for %q, "+
				"which it was not asked about", i+1, of, a.Name))
			continue
		}
		got.sentences[key] = sentence
		info(opts.Logger, "describe: "+key+"  "+sentence)
	}
	return got
}

// requested indexes a batch's keys by their lower-cased spelling, so an answer
// is matched back case-insensitively without letting a name through that was
// never asked for.
func requested(batch []Item) map[string]string {
	asked := make(map[string]string, len(batch))
	for _, item := range batch {
		asked[strings.ToLower(item.Key)] = item.Key
	}
	return asked
}

// resolve maps one answer's name onto the key it was asked about. A model that
// drops the schema qualifier is still understood, as long as exactly one key in
// the batch ends that way.
func resolve(asked map[string]string, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false
	}
	if key, ok := asked[strings.ToLower(name)]; ok {
		return key, true
	}
	suffix := "." + strings.ToLower(name)
	found := ""
	for lowered, key := range asked {
		if strings.HasSuffix(lowered, suffix) {
			if found != "" {
				return "", false
			}
			found = key
		}
	}
	return found, found != ""
}

// run calls body for every index, workers at a time, and returns once all of
// them have. One worker runs them in order on this goroutine.
func run(n, workers int, body func(i int)) {
	if workers <= 1 || n <= 1 {
		for i := 0; i < n; i++ {
			body(i)
		}
		return
	}
	if workers > n {
		workers = n
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				body(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// plural spells a count with its noun, so a log line reads as a sentence.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ch") {
		return strconv.Itoa(n) + " " + noun + "es"
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func info(logger Logger, message string) {
	if logger != nil {
		logger.Info(message)
	}
}

func warn(logger Logger, message string) {
	if logger != nil {
		logger.Warn(message)
	}
}
