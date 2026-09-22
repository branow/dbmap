package index

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/branow/dbmap/internal/catalog"
	"github.com/branow/dbmap/internal/engine"
	"github.com/branow/dbmap/internal/redact"
	"github.com/branow/dbmap/llm"
)

// database is a whole database with nothing real behind it. It records every
// call, so a test asserts on what the pipeline sent rather than on what it
// returned.
type database struct {
	objects    []catalog.Object
	structures map[string]catalog.Structure
	bodies     map[string]string
	rows       map[string][][]string

	// health, when set, is returned instead of a healthy reading.
	health *engine.Health

	manifests int
	fetches   int
	modules   []string
	sampled   []string
	probes    []string
	closed    bool
}

func (d *database) Conn() engine.Conn { return nil }

func (d *database) Close() error {
	d.closed = true
	return nil
}

func (d *database) Manifest(context.Context, engine.Conn) ([]catalog.Object, error) {
	d.manifests++
	return d.objects, nil
}

func (d *database) Structure(context.Context, engine.Conn) (map[string]catalog.Structure, error) {
	d.fetches++
	out := map[string]catalog.Structure{}
	for key, value := range d.structures {
		out[key] = value
	}
	return out, nil
}

func (d *database) Modules(_ context.Context, _ engine.Conn,
	keys []string) (map[string]redact.Body, error) {
	d.modules = append(d.modules, keys...)
	bodies := map[string]redact.Body{}
	for _, key := range keys {
		if body, ok := d.bodies[key]; ok {
			bodies[key] = redact.Text(body)
		}
	}
	return bodies, nil
}

func (d *database) Sample(_ context.Context, _ engine.Conn, table engine.Table,
	n int) (catalog.Sample, error) {
	d.sampled = append(d.sampled, table.Key())
	rows := d.rows[table.Key()]
	if len(rows) > n {
		rows = rows[:n]
	}
	return catalog.Sample{Key: table.Key(), Columns: table.Columns, Rows: rows}, nil
}

func (d *database) Health(_ context.Context, _ engine.Conn) (engine.Health, error) {
	d.probes = append(d.probes, "health")
	if d.health != nil {
		return *d.health, nil
	}
	return engine.Classify(&engine.Reading{}), nil
}

// queries is every read the build sent beyond the health probe. A dry run must
// leave this at one: the manifest and nothing else.
func (d *database) queries() int {
	return d.manifests + d.fetches + len(d.modules) + len(d.sampled)
}

// model answers every batch with a sentence per object it was given, and
// records what it was asked and whether the database was still open at the
// time.
type model struct {
	source *database
	calls  int
	// live records whether a connection was still open when a call arrived. It
	// must stay false: the describe stage never holds one.
	live  bool
	fail  error
	blank bool
}

func (m *model) Name() string { return "fake" }

func (m *model) Complete(_ context.Context, req llm.Request) (*llm.Response, error) {
	m.calls++
	if m.source != nil && !m.source.closed {
		m.live = true
	}
	if m.fail != nil {
		return nil, m.fail
	}

	type answer struct {
		Name     string `json:"name"`
		Sentence string `json:"sentence"`
	}
	var answers []answer
	for _, line := range strings.Split(req.Prompt, "\n") {
		if !strings.HasPrefix(line, "=== OBJECT ") {
			continue
		}
		key := strings.TrimSpace(strings.TrimSuffix(line[strings.Index(line, ":")+1:], "==="))
		if m.blank {
			continue
		}
		answers = append(answers, answer{Name: key, Sentence: "Describes " + key + "."})
	}
	body, err := json.Marshal(map[string]any{"objects": answers})
	if err != nil {
		return nil, err
	}
	return &llm.Response{
		Structured: body,
		Model:      "fake-model",
		Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

// recorder collects the build's progress lines.
type recorder struct{ lines []string }

func (r *recorder) Info(message string) { r.lines = append(r.lines, message) }
func (r *recorder) Warn(message string) { r.lines = append(r.lines, message) }

// table is the one database every test starts from: two tables, one procedure,
// one view and one synonym.
func table() *database {
	return &database{
		objects: []catalog.Object{
			{Schema: "dbo", Name: "Orders", Kind: catalog.Table, Rows: 4000,
				KB: 900, Modified: "2024-01-01"},
			{Schema: "dbo", Name: "OrderStatuses", Kind: catalog.Table, Rows: 11,
				KB: 8, Modified: "2024-01-01"},
			{Schema: "dbo", Name: "OrderGet", Kind: catalog.Procedure, Modified: "2024-01-01"},
			{Schema: "dbo", Name: "OpenOrders", Kind: catalog.View, Modified: "2024-01-01"},
			{Schema: "dbo", Name: "OrderAlias", Kind: catalog.Synonym, Modified: "2024-01-01"},
		},
		structures: map[string]catalog.Structure{
			"dbo.Orders": {
				Columns: []catalog.Column{
					{Name: "OrderID", Type: "int", Identity: true},
					{Name: "StatusID", Type: "int"},
				},
				PrimaryKey: []string{"OrderID"},
			},
			"dbo.OrderStatuses": {
				Columns: []catalog.Column{
					{Name: "StatusID", Type: "int"},
					{Name: "Label", Type: "nvarchar", Length: "(40)"},
				},
			},
			"dbo.OpenOrders": {
				Columns: []catalog.Column{{Name: "OrderID", Type: "int"}},
			},
			"dbo.OrderAlias": {Target: "dbo.Orders"},
		},
		bodies: map[string]string{
			"dbo.OrderGet":   "SELECT * FROM dbo.Orders WHERE OrderID = @id",
			"dbo.OpenOrders": "SELECT OrderID FROM dbo.Orders WHERE StatusID = 1",
		},
		rows: map[string][][]string{
			"dbo.OrderStatuses": {{"1", "Pending"}, {"2", "Shipped"}},
		},
	}
}

// signal moves one object's modify signal, which is what a redeploy does.
func (d *database) signal(key, value string) {
	for i, object := range d.objects {
		if object.Key() == key {
			d.objects[i].Modified = catalog.Signal(value)
			return
		}
	}
}

// drop removes an object from the source, as a DROP would.
func (d *database) drop(key string) {
	var kept []catalog.Object
	for _, object := range d.objects {
		if object.Key() != key {
			kept = append(kept, object)
		}
	}
	d.objects = kept
	delete(d.structures, key)
	delete(d.bodies, key)
}

// reset clears the call record between runs of the same fake database.
func (d *database) reset() {
	d.manifests, d.fetches = 0, 0
	d.modules, d.sampled, d.probes = nil, nil, nil
	d.closed = false
}

func mustBuild(t *testing.T, src Source, client llm.Client, opts Options) Summary {
	t.Helper()
	summary, err := Build(context.Background(), src, client, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return summary
}

var errModel = errors.New("model refused")
