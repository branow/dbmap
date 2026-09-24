package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/branow/dbmap/llm"
)

// Batches are sized by prompt characters, then by count.
const (
	BatchChars = 40000
	BatchMax   = 12
)

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
// Result rather than aborting the run.
func All(ctx context.Context, client llm.Client, inputs []Input, opts Options) (Result, error) {
	result := Result{Sentences: map[string]string{}}

	items, err := Prepare(inputs)
	if err != nil {
		return result, err
	}
	batches := Batch(items, opts.chars(), opts.max())

	for i, batch := range batches {
		response, err := client.Complete(ctx, llm.Request{
			Prompt: BatchPrompt(batch),
			Schema: Schema,
			Model:  opts.Model,
		})
		if err != nil {
			result.Failed = append(result.Failed, err)
			warn(opts.Logger, fmt.Sprintf("describe: batch %d/%d failed: %v", i+1, len(batches), err))
			continue
		}
		result.Usage.Add(response.Usage)

		var parsed reply
		if err := json.Unmarshal(response.Structured, &parsed); err != nil {
			result.Failed = append(result.Failed, err)
			warn(opts.Logger, fmt.Sprintf("describe: batch %d/%d returned unreadable output: %v", i+1, len(batches), err))
			continue
		}

		// Matched by name, never by position - and resolved against the keys
		// this batch actually asked about, never trusted as written.
		asked := requested(batch)
		for _, a := range parsed.Objects {
			sentence := strings.TrimSpace(a.Sentence)
			if sentence == "" {
				continue
			}
			key, ok := resolve(asked, a.Name)
			if !ok {
				// Filing the sentence under the name the model wrote would count
				// it as described while the object it was asked about reports
				// missing - both at once, from the same batch.
				warn(opts.Logger, fmt.Sprintf("describe: batch %d/%d answered for %q, "+
					"which it was not asked about", i+1, len(batches), a.Name))
				continue
			}
			result.Sentences[key] = sentence
		}
		info(opts.Logger, fmt.Sprintf("describe: batch %d/%d (%d objects)", i+1, len(batches), len(batch)))
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
