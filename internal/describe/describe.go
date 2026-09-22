package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/branow/dbmap/llm"
)

// Budget sizes one batch. Objects are wildly uneven — a 200-line procedure and
// a two-column lookup table are not the same unit of work — so a batch is
// measured in prompt characters first and object count second.
//
// Roughly 100 calls for 1,237 objects instead of 1,237.
const (
	BatchChars = 40000
	BatchMax   = 12
)

// sentenceField is the load-bearing wording. The field is `sentence`, and its
// schema text is an ORDER rather than a noun phrase.
//
// Named `description` with the text "one sentence saying what this object
// does", every model tested filled it with a description of the field instead
// of an answer, and the better the model followed instructions the more
// reliably it did so. Renaming the field and rewriting the text as an
// instruction fixed every case. Do not soften either; a test fails if you do.
const sentenceField = "Put the finished sentence here verbatim. Never describe what the sentence would say."

// Schema is the structured output one batch must return. Answers carry the
// object name so they can be matched BY NAME rather than by position: a model
// that reorders its reply must still land every sentence on the right object.
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
          },
          "lookup": {
            "type": "boolean",
            "description": "For tables only: true when the table is a lookup of codes and their meanings."
          }
        }
      }
    }
  }
}`)

// answer is one object's reply.
type answer struct {
	Name     string `json:"name"`
	Sentence string `json:"sentence"`
	Lookup   bool   `json:"lookup"`
}

type reply struct {
	Objects []answer `json:"objects"`
}

// Result is what a run produced: a sentence per object it could describe, the
// lookup tables it identified, and what it could not do.
type Result struct {
	Sentences map[string]string
	Lookups   map[string]bool
	// Missing lists objects the model skipped. They are reported rather than
	// left silently blank, because a blank description in the index looks like
	// a described object with nothing to say.
	Missing []string
	// Failed lists batches that errored. A failed batch is skipped, never
	// fatal: a partial index beats none.
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
	Model  string
	Chars  int
	Max    int
	Logger Logger
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

// Batch groups items by cost. An item over the character budget goes alone
// rather than being dropped — an object too big to share a call is still an
// object that needs describing.
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

// All describes every input it can, in batches.
//
// A batch that fails is logged and skipped rather than aborting the run: a
// partial index is worth more than none, and the failure is reported on Result
// so a caller can decide otherwise.
func All(ctx context.Context, client llm.Client, inputs []Input, opts Options) (Result, error) {
	result := Result{Sentences: map[string]string{}, Lookups: map[string]bool{}}

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

		// Matched by name, never by position.
		for _, a := range parsed.Objects {
			sentence := strings.TrimSpace(a.Sentence)
			if a.Name == "" || sentence == "" {
				continue
			}
			result.Sentences[a.Name] = sentence
			if a.Lookup {
				result.Lookups[a.Name] = true
			}
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
