package lang_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/andreyvit/diff"
	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/flux"
	"github.com/influxdata/flux/ast"
	fcsv "github.com/influxdata/flux/csv"
	"github.com/influxdata/flux/dependencies/dependenciestest"
	"github.com/influxdata/flux/dependency"
	"github.com/influxdata/flux/execute"
	"github.com/influxdata/flux/execute/executetest"
	_ "github.com/influxdata/flux/fluxinit/static"
	"github.com/influxdata/flux/lang"
	"github.com/influxdata/flux/memory"
	"github.com/influxdata/flux/mock"
	"github.com/influxdata/flux/parser"
	"github.com/influxdata/flux/plan"
	"github.com/influxdata/flux/plan/plantest"
	"github.com/influxdata/flux/runtime"
	"github.com/influxdata/flux/stdlib/csv"
	"github.com/influxdata/flux/stdlib/influxdata/influxdb"
	"github.com/influxdata/flux/stdlib/universe"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/mocktracer"
)

func init() {
	execute.RegisterSource(influxdb.FromKind, mock.CreateMockFromSource)
	plan.RegisterLogicalRules(
		influxdb.DefaultFromAttributes{
			Org:  &influxdb.NameOrID{Name: "influxdata"},
			Host: func(v string) *string { return &v }("http://localhost:8086"),
		},
	)
}

func TestFluxCompiler(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name         string
		now          time.Time
		extern       *ast.File
		externRaw    json.RawMessage
		q            string
		jsonCompiler []byte
		compilerErr  string
		startErr     string
	}{
		{
			name: "simple",
			q:    `from(bucket: "foo") |> range(start: -5m)`,
		},
		{
			name:        "syntax error",
			q:           `t={]`,
			compilerErr: "expected RBRACE",
		},
		{
			name:     "error",
			q:        `t=0 t.s`,
			startErr: "error @1:5-1:6",
		},
		{
			name:     "from with no streaming data",
			q:        `x = from(bucket: "foo") |> range(start: -5m)`,
			startErr: "no streaming data",
		},
		{
			name: "from with yield",
			q:    `x = from(bucket: "foo") |> range(start: -5m) |> yield()`,
		},
		{
			name: "extern",
			extern: &ast.File{
				Body: []ast.Statement{
					&ast.OptionStatement{
						Assignment: &ast.VariableAssignment{
							ID:   &ast.Identifier{Name: "twentySix"},
							Init: &ast.IntegerLiteral{Value: 26},
						},
					},
				},
			},
			q: `twentySeven = twentySix + 1
				twentySeven
				from(bucket: "foo") |> range(start: -5m)`,
		},
		{
			name: "extern with error",
			extern: &ast.File{
				Body: []ast.Statement{
					&ast.OptionStatement{
						Assignment: &ast.VariableAssignment{
							ID:   &ast.Identifier{Name: "twentySix"},
							Init: &ast.IntegerLiteral{Value: 26},
						},
					},
				},
			},
			q: `twentySeven = twentyFive + 2
				twentySeven
				from(bucket: "foo") |> range(start: -5m)`,
			startErr: "undefined identifier twentyFive",
		},
		{
			name:        "extern invalid json",
			externRaw:   []byte(`{"type":"File","package":null,"imports":null,"body":[{"type":"OptionStatement","assignment":{"type":"VariableAssignment","id":{"type":"Identifier","name":"period"},"init":{"type":"DurationLiteral","values":[{"magnitude":null,"unit":"ms"}]}}}]}`),
			q:           `from(bucket: "telegraf") |> range(start: -5m)`,
			compilerErr: `extern json parse error: invalid type: null, expected i64 at line 1 column 286`,
		},
		{
			name: "with now",
			now:  time.Unix(1000, 0),
			q:    `from(bucket: "foo") |> range(start: -5m)`,
		},
		{
			name: "extern that uses null keyword",
			now:  parser.MustParseTime("2020-03-24T14:24:46.15933241Z").Value,
			jsonCompiler: []byte(`
{
    "Now": "2020-03-24T14:24:46.15933241Z",
    "extern": null,
    "query": "from(bucket: \"apps\")\n  |> range(start: -30s)\n  |> filter(fn: (r) => r._measurement == \"query_control_queueing_active\")\n  |> filter(fn: (r) => r._field == \"gauge\")\n  |> filter(fn: (r) => r.env == \"acc\")\n  |> group(columns: [\"host\"])\n  |> last()\n  |> group()\n  |> mean()\n  // Rename \"_value\" to \"metricValue\" for properly unmarshaling the result.\n  |> rename(columns: {_value: \"metricvalue\"})\n  |> keep(columns: [\"metricvalue\"])\n"
}`),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var c lang.FluxCompiler
			{
				if tc.q != "" {
					if tc.extern != nil {
						var err error
						tc.externRaw, err = json.Marshal(tc.extern)
						if err != nil {
							t.Fatal(err)
						}
					}
					c = lang.FluxCompiler{
						Now:    tc.now,
						Extern: tc.externRaw,
						Query:  tc.q,
					}
				} else if len(tc.jsonCompiler) > 0 {
					if err := json.Unmarshal(tc.jsonCompiler, &c); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Fatal("expected either a query, or a jsonCompiler in test case")
				}
			}
			// serialize and deserialize and make sure they are equal
			bs, err := json.Marshal(c)
			if err != nil {
				t.Error(err)
			}
			cc := lang.FluxCompiler{}
			err = json.Unmarshal(bs, &cc)
			if err != nil {
				t.Error(err)
			}
			if diff := cmp.Diff(c, cc); diff != "" {
				t.Errorf("compiler serialized/deserialized does not match: -want/+got:\n%v", diff)
			}

			program, err := c.Compile(ctx, runtime.Default)
			if err != nil {
				if tc.compilerErr != "" {
					if !strings.Contains(err.Error(), tc.compilerErr) {
						t.Fatalf(`expected query to error with "%v" but got "%v"`, tc.compilerErr, err)
					} else {
						return
					}
				}
				t.Fatalf("failed to compile AST: %v", err)
			} else if tc.compilerErr != "" {
				t.Fatalf("expected query to error with %q, but got no error", tc.compilerErr)
			}

			astProg := program.(*lang.AstProgram)
			if astProg.Now != tc.now {
				t.Errorf(`unexpected value for now, want "%v", got "%v"`, tc.now, astProg.Now)
			}

			// we need to start the program to get compile errors derived from AST evaluation
			ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
			defer deps.Finish()

			if _, err = program.Start(ctx, &memory.ResourceAllocator{}); tc.startErr == "" && err != nil {
				t.Errorf("expected query %q to start successfully but got error %v", tc.q, err)
			} else if tc.startErr != "" && err == nil {
				t.Errorf("expected query %q to start with error but got no error", tc.q)
			} else if tc.startErr != "" && err != nil && !strings.Contains(err.Error(), tc.startErr) {
				t.Errorf(`expected query to error with "%v" but got "%v"`, tc.startErr, err)
			}
		})
	}
}

func TestCompilationError(t *testing.T) {
	program, err := lang.Compile(`illegal query`, runtime.Default, time.Unix(0, 0))
	if err != nil {
		// This shouldn't happen, has the script should be evaluated at program Start.
		t.Fatal(err)
	}

	ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
	defer deps.Finish()

	_, err = program.Start(ctx, &memory.ResourceAllocator{})
	if err == nil {
		t.Fatal("compilation error expected, got none")
	}
}

func TestASTCompiler(t *testing.T) {
	testcases := []struct {
		name         string
		now          time.Time
		file         *ast.File
		script       string
		jsonCompiler []byte
		want         plantest.PlanSpec
		startErr     string
	}{
		{
			name: "override now time using now option",
			now:  time.Unix(1, 1),
			script: `
import "csv"
option now = () => 2017-10-10T00:01:00Z
csv.from(csv: "foo,bar") |> range(start: 2017-10-10T00:00:00Z)
`,
			want: plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &csv.FromCSVProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.RangeProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
				},
				Now: parser.MustParseTime("2017-10-10T00:01:00Z").Value,
			},
		},
		{
			name: "get now time from compiler",
			now:  parser.MustParseTime("2018-10-10T00:00:00Z").Value,
			script: `
import "csv"
csv.from(csv: "foo,bar") |> range(start: 2017-10-10T00:00:00Z)
`,
			want: plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &csv.FromCSVProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.RangeProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
				},
				Now: parser.MustParseTime("2018-10-10T00:00:00Z").Value,
			},
		},
		{
			name: "extern",
			file: &ast.File{
				Body: []ast.Statement{
					&ast.OptionStatement{
						Assignment: &ast.VariableAssignment{
							ID: &ast.Identifier{Name: "now"},
							Init: &ast.FunctionExpression{
								Body: &ast.DateTimeLiteral{
									Value: parser.MustParseTime("2018-10-10T00:00:00Z").Value,
								},
							},
						},
					},
				},
			},
			script: `
import "csv"
csv.from(csv: "foo,bar") |> range(start: 2017-10-10T00:00:00Z)
`,
			want: plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &csv.FromCSVProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.RangeProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
				},
				Now: parser.MustParseTime("2018-10-10T00:00:00Z").Value,
			},
		},
		{
			name:     "simple case",
			now:      parser.MustParseTime("2018-10-10T00:00:00Z").Value,
			script:   `x = 1`,
			startErr: "no streaming data",
		},
		{
			name: "json compiler with null keyword",
			jsonCompiler: []byte(`
{
  "extern": null,
  "ast": {
    "type": "Package",
    "package": "main",
    "files": [
      {
        "type": "File",
        "metadata": "parser-type=rust",
        "package": null,
        "imports": [],
        "body": [
          {
            "type": "VariableAssignment",
            "id": {
              "name": "x"
            },
            "init": {
              "type": "IntegerLiteral",
              "value": "1"
            }
          }
        ]
      }
    ]
  },
  "Now": "2018-10-10T00:00:00Z"
}
`),
			startErr: "no streaming data",
		},
	}
	rt := runtime.Default
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			var c lang.ASTCompiler
			{
				if tc.script != "" {
					astPkg, err := rt.Parse(tc.script)
					if err != nil {
						t.Fatalf("failed to parse script: %v", err)
					}
					var jsonPkg json.RawMessage
					jsonPkg, err = parser.HandleToJSON(astPkg)
					if err != nil {
						t.Fatal(err)
					}

					// The JSON produced by Rust does not escape characters like ">", but
					// Go does, so we need to use HTMLEscape to make the roundtrip the same.
					var buf bytes.Buffer
					json.HTMLEscape(&buf, jsonPkg)
					jsonPkg = buf.Bytes()

					c = lang.ASTCompiler{
						AST: jsonPkg,
						Now: tc.now,
					}

					if tc.file != nil {
						bs, err := json.Marshal(tc.file)
						if err != nil {
							t.Fatal(err)
						}
						c.Extern = bs
					}
				} else if len(tc.jsonCompiler) > 0 {
					var bb bytes.Buffer
					if err := json.Compact(&bb, tc.jsonCompiler); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(bb.Bytes(), &c); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Fatal("expected either script of jsonCompiler in test case")
				}
			}
			// serialize and deserialize and make sure they are equal
			bs, err := json.Marshal(c)
			if err != nil {
				t.Error(err)
			}
			cc := lang.ASTCompiler{}
			err = json.Unmarshal(bs, &cc)
			if err != nil {
				t.Error(err)
			}
			if diff := cmp.Diff(c, cc); diff != "" {
				t.Errorf("compiler serialized/deserialized does not match: -want/+got:\n%v", diff)
			}

			program, err := c.Compile(context.Background(), runtime.Default)
			if err != nil {
				t.Fatalf("failed to compile AST: %v", err)
			}
			ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
			defer deps.Finish()

			// we need to start the program to get compile errors derived from AST evaluation
			if _, err := program.Start(ctx, &memory.ResourceAllocator{}); err != nil {
				if tc.startErr == "" {
					t.Fatalf("failed to start program: %v", err)
				} else {
					// We expect an error, did we get the right one?
					if !strings.Contains(err.Error(), tc.startErr) {
						t.Fatalf("expected to get an error containing %q but got %q", tc.startErr, err.Error())
					}
					return
				}
			}

			got := program.(*lang.AstProgram).PlanSpec
			want := plantest.CreatePlanSpec(&tc.want)
			if err := plantest.ComparePlansShallow(want, got); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestASTCompiler_ShadowNowVariable(t *testing.T) {
	now, err := time.Parse(time.RFC3339, "2020-12-04T13:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	c := &lang.FluxCompiler{
		Query: `package main
import "csv"
now = 5.0
csv.from(csv: "
#datatype,string,long,dateTime:RFC3339,double
#group,false,false,false,false
#default,_result,,,
,result,table,_time,_value
,,0,2020-12-04T09:00:00Z,2.0
,,0,2020-12-04T10:00:00Z,8.0
,,0,2020-12-04T11:00:00Z,${string(v: now)}
,,0,2020-12-04T12:00:00Z,3.0
") |> range(start: -2h)
`,
		Now: now,
	}
	program, err := c.Compile(context.Background(), runtime.Default)
	if err != nil {
		t.Fatalf("unexpected compile error: %s", err)
	}

	mem := &memory.ResourceAllocator{}
	qry, err := program.Start(context.Background(), mem)
	if err != nil {
		t.Fatalf("unexpected program error: %s", err)
	}

	results := flux.NewResultIteratorFromQuery(qry)
	defer results.Release()

	var gotB strings.Builder
	enc := fcsv.NewMultiResultEncoder(fcsv.DefaultEncoderConfig())
	if _, err := enc.Encode(&gotB, results); err != nil {
		t.Fatalf("unexpected encode error: %s", err)
	}
	want, got := toCRLF(`#datatype,string,long,dateTime:RFC3339,dateTime:RFC3339,dateTime:RFC3339,double
#group,false,false,true,true,false,false
#default,_result,,,,,
,result,table,_start,_stop,_time,_value
,,0,2020-12-04T11:00:00Z,2020-12-04T13:00:00Z,2020-12-04T11:00:00Z,5
,,0,2020-12-04T11:00:00Z,2020-12-04T13:00:00Z,2020-12-04T12:00:00Z,3

`), gotB.String()
	if want != got {
		t.Fatalf("unexpected output -want/+got:\n%s", diff.LineDiff(want, got))
	}
}

func TestCompileOptions(t *testing.T) {
	src := `import "csv"
			csv.from(csv: "foo,bar")
				|> range(start: 2017-10-10T00:00:00Z)
				|> count()`

	now := parser.MustParseTime("2018-10-10T00:00:00Z").Value

	opt := lang.WithLogPlanOpts(plan.OnlyLogicalRules(removeCount{}))

	program, err := lang.Compile(src, runtime.Default, now, opt)
	if err != nil {
		t.Fatalf("failed to compile script: %v", err)
	}

	// start program in order to evaluate planner options
	ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
	defer deps.Finish()

	if _, err := program.Start(ctx, &memory.ResourceAllocator{}); err != nil {
		t.Fatalf("failed to start program: %v", err)
	}

	want := plantest.CreatePlanSpec(&plantest.PlanSpec{
		Nodes: []plan.Node{
			&plan.PhysicalPlanNode{Spec: &csv.FromCSVProcedureSpec{}},
			&plan.PhysicalPlanNode{Spec: &universe.RangeProcedureSpec{}},
		},
		Edges: [][2]int{
			{0, 1},
		},
		Now: parser.MustParseTime("2018-10-10T00:00:00Z").Value,
	})

	if err := plantest.ComparePlansShallow(want, program.PlanSpec); err != nil {
		t.Fatalf("unexpected plans: %v", err)
	}
}

type removeCount struct{}

func (rule removeCount) Name() string {
	return "removeCountRule"
}
func (rule removeCount) Pattern() plan.Pattern {
	return plan.Pat(universe.CountKind, plan.Any())
}
func (rule removeCount) Rewrite(ctx context.Context, node plan.Node) (plan.Node, bool, error) {
	return node.Predecessors()[0], true, nil
}

func TestCompileOptions_FromFluxOptions(t *testing.T) {
	nowFn := func() time.Time {
		return parser.MustParseTime("2018-10-10T00:00:00Z").Value
	}
	// If this test is run with a count of greater than one, we panic here
	// because removeCount is already registered.
	plan.RegisterLogicalRules(&removeCount{})

	tcs := []struct {
		name    string
		files   []string
		want    *plan.Spec
		wantErr string
	}{
		{
			name: "no planner option set",
			files: []string{`
import "planner"

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
				},
				Edges: [][2]int{},
				Now:   nowFn(),
			}),
		},
		{
			name: "remove push down filter",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = ["influxdata/influxdb.MergeRemoteFilterRule"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.FilterProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
				},
				Now: nowFn(),
			}),
		},
		{
			name: "remove push down filter and count",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = ["influxdata/influxdb.MergeRemoteFilterRule"]
option planner.disableLogicalRules = ["removeCountRule"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.FilterProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.CountProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
					{1, 2},
				},
				Now: nowFn(),
			}),
		},
		{
			name: "remove push down filter and count - with non existent rule",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = ["influxdata/influxdb.MergeRemoteFilterRule", "non_existent"]
option planner.disableLogicalRules = ["removeCountRule", "non_existent"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.FilterProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.CountProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
					{1, 2},
				},
				Now: nowFn(),
			}),
		},
		{
			name: "remove non existent rules does not produce any effect",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = ["foo", "bar", "mew", "buz", "foxtrot"]
option planner.disableLogicalRules = ["foo", "bar", "mew", "buz", "foxtrot"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
				},
				Edges: [][2]int{},
				Now:   nowFn(),
			}),
		},
		{
			name: "empty planner option does not produce any effect",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = [""]
option planner.disableLogicalRules = [""]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
				},
				Edges: [][2]int{},
				Now:   nowFn(),
			}),
		},
		{
			name: "logical planner option must be an array",
			files: []string{`
import "planner"

option planner.disableLogicalRules = "not an array"

// remember to return streaming data
from(bucket: "does_not_matter")`},
			wantErr: `error @4:38-4:52: expected [string] (array) but found string`,
		},
		{
			name: "physical planner option must be an array",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = "not an array"

// remember to return streaming data
from(bucket: "does_not_matter")`},
			wantErr: `error @4:39-4:53: expected [string] (array) but found string`,
		},
		{
			name: "logical planner option must be an array of strings",
			files: []string{`
import "planner"

option planner.disableLogicalRules = [1.0]

// remember to return streaming data
from(bucket: "does_not_matter")`},
			wantErr: `error @4:38-4:43: expected string but found float`,
		},
		{
			name: "physical planner option must be an array of strings",
			files: []string{`
import "planner"

option planner.disablePhysicalRules = [1.0]

// remember to return streaming data
from(bucket: "does_not_matter")`},
			wantErr: `error @4:39-4:44: expected string but found float`,
		},
		{
			name: "planner is an object defined by the user",
			files: []string{`
planner = {
	disablePhysicalRules: ["fromRangeRule"],
	disableLogicalRules: ["removeCountRule"]
}

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			// This shouldn't change the plan.
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
				},
				Edges: [][2]int{},
				Now:   nowFn(),
			}),
		},
		{
			name: "use planner option with alias",
			files: []string{`
import pl "planner"

option pl.disablePhysicalRules = ["influxdata/influxdb.MergeRemoteFilterRule"]
option pl.disableLogicalRules = ["removeCountRule"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.FilterProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.CountProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
					{1, 2},
				},
				Now: nowFn(),
			}),
		},
		{
			name: "multiple files - splitting options setting",
			files: []string{
				`package main
import pl "planner"

option pl.disablePhysicalRules = ["influxdata/influxdb.MergeRemoteFilterRule"]

from(bucket: "bkt") |> range(start: 0) |> filter(fn: (r) => r._value > 0) |> count()`,
				`package foo
import "planner"

option planner.disableLogicalRules = ["removeCountRule"]`},
			want: plantest.CreatePlanSpec(&plantest.PlanSpec{
				Nodes: []plan.Node{
					&plan.PhysicalPlanNode{Spec: &influxdb.FromRemoteProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.FilterProcedureSpec{}},
					&plan.PhysicalPlanNode{Spec: &universe.CountProcedureSpec{}},
				},
				Edges: [][2]int{
					{0, 1},
					{1, 2},
				},
				Now: nowFn(),
			}),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "multiple files") {
				t.Skip("how should options behave with multiple files?")
			}

			if len(tc.files) == 0 {
				t.Fatal("the test should have at least one file")
			}
			astPkg, err := runtime.Parse(tc.files[0])
			if err != nil {
				t.Fatal(err)
			}

			if len(tc.files) > 1 {
				for _, file := range tc.files[1:] {
					otherPkg, err := runtime.Parse(file)
					if err != nil {
						t.Fatal(err)
					}
					if err := runtime.MergePackages(astPkg, otherPkg); err != nil {
						t.Fatal(err)
					}
				}
			}

			program := lang.CompileAST(astPkg, runtime.Default, nowFn())
			ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
			defer deps.Finish()

			if q, err := program.Start(ctx, &memory.ResourceAllocator{}); err != nil {
				if tc.wantErr == "" {
					t.Fatalf("failed to start program: %v", err)
				} else if got := getRootErr(err); tc.wantErr != got.Error() {
					t.Fatalf("expected wrong error -want/+got:\n\t- %s\n\t+ %s", tc.wantErr, got)
				}
				return
			} else {
				q.Done()
				if tc.wantErr != "" {
					t.Fatalf("expected error, got none")
				}
			}

			if err := plantest.ComparePlansShallow(tc.want, program.PlanSpec); err != nil {
				t.Errorf("unexpected plans: %v", err)
			}
		})
	}
}

func TestQueryTracing(t *testing.T) {
	// temporarily install a mock tracer to see which spans are created.
	oldTracer := opentracing.GlobalTracer()
	defer opentracing.SetGlobalTracer(oldTracer)
	mockTracer := mocktracer.New()
	opentracing.SetGlobalTracer(mockTracer)

	ctx := context.Background()

	// Run a query
	c := lang.FluxCompiler{
		Query: `
			import "array"
			array.from(rows: [{key: 1, value: 2}, {key: 3, value: 2}])
			  |> filter(fn: (r) => r.value == 2)
			  |> group(columns: ["key"])
			  |> map(fn: (r) => ({r with foo: "hi"}))`,
	}

	prog, err := c.Compile(ctx, runtime.Default)
	if err != nil {
		t.Fatal(err)
	}
	q, err := prog.Start(ctx, memory.DefaultAllocator)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Done()
	for r := range q.Results() {
		if err := r.Tables().Do(func(flux.Table) error {
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.Err(); err != nil {
		t.Fatal(err)
	}
	q.Done()

	// If tracing was enabled, then we should see spans for each
	// transformation.
	var executeSpanId int
	for _, span := range mockTracer.FinishedSpans() {
		if span.OperationName == "execute" {
			if executeSpanId != 0 {
				t.Errorf("found multiple spans for operation %q", "execute")
			}
			executeSpanId = span.SpanContext.SpanID
		}
	}
	if executeSpanId == 0 {
		t.Errorf("did not find %q span", "execute")
	}

	wantSpans := []struct {
		opName   string
		msgCount int
	}{
		{opName: "*universe.filterTransformation", msgCount: 2},
		{opName: "*universe.groupTransformation", msgCount: 3},
		{opName: "*universe.mapTransformation", msgCount: 3},
	}
	for _, wantSpan := range wantSpans {
		var gotSpan *mocktracer.MockSpan
		for _, sp := range mockTracer.FinishedSpans() {
			if wantSpan.opName == sp.OperationName {
				gotSpan = sp
				break
			}
		}
		if gotSpan == nil {
			t.Fatalf("did not find span for operation %v", wantSpan.opName)
		}
		// Make sure the parent ID is the execute span
		if gotSpan.ParentID != executeSpanId {
			t.Errorf("expected span for %q to have execute parent ID of %v but it was %v", wantSpan.opName, executeSpanId, gotSpan.ParentID)
		}

		var msgCount *mocktracer.MockKeyValue
		for _, lr := range gotSpan.Logs() {
			for _, kv := range lr.Fields {
				if kv.Key == "messages_processed" {
					msgCount = &kv
					break
				}
			}
			if msgCount != nil {
				break
			}
		}
		if msgCount == nil {
			t.Fatalf("did not find %q log for operation %q", "messages_processed", wantSpan.opName)
		}
		if want, got := fmt.Sprintf("%d", wantSpan.msgCount), msgCount.ValueString; want != got {
			t.Errorf("got unexpected message count for operation %q; -want/+got: %v/%v ", wantSpan.opName, want, got)
		}
	}
}

func getRootErr(err error) error {
	if err == nil {
		return err
	}
	fe, ok := err.(*flux.Error)
	if !ok {
		return err
	}
	if fe == nil {
		return fe
	}
	if fe.Err == nil {
		return fe
	}
	return getRootErr(fe.Err)
}

// TestTableObjectCompiler evaluates a simple `from |> range |> filter` script on csv data, and
// extracts the TableObjects obtained from evaluation. It eventually compiles TableObjects and
// compares obtained results with expected ones (obtained from decoding the raw csv data).
func TestTableObjectCompiler(t *testing.T) {
	dataRaw := `#datatype,string,long,dateTime:RFC3339,long,string,string,string,string
#group,false,false,false,false,false,false,true,true
#default,_result,,,,,,,
,result,table,_time,_value,_field,_measurement,host,name
,,0,2018-05-22T19:53:26Z,15204688,io_time,diskio,host.local,disk0
,,0,2018-05-22T19:53:36Z,15204894,io_time,diskio,host.local,disk0
,,0,2018-05-22T19:53:46Z,15205102,io_time,diskio,host.local,disk0
,,0,2018-05-22T19:53:56Z,15205226,io_time,diskio,host.local,disk0
,,0,2018-05-22T19:54:06Z,15205499,io_time,diskio,host.local,disk0
,,0,2018-05-22T19:54:16Z,15205755,io_time,diskio,host.local,disk0
,,1,2018-05-22T19:53:26Z,648,io_time,diskio,host.local,disk2
,,1,2018-05-22T19:53:36Z,648,io_time,diskio,host.local,disk2
,,1,2018-05-22T19:53:46Z,648,io_time,diskio,host.local,disk2
,,1,2018-05-22T19:53:56Z,648,io_time,diskio,host.local,disk2
,,1,2018-05-22T19:54:06Z,648,io_time,diskio,host.local,disk2
,,1,2018-05-22T19:54:16Z,648,io_time,diskio,host.local,disk2
`

	rangedDataRaw := `#datatype,string,long,dateTime:RFC3339,dateTime:RFC3339,dateTime:RFC3339,long,string,string,string,string
#group,false,false,true,true,false,false,false,false,true,true
#default,_result,,,,,,,,,
,result,table,_start,_stop,_time,_value,_field,_measurement,host,name
,,0,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:26Z,15204688,io_time,diskio,host.local,disk0
,,0,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:36Z,15204894,io_time,diskio,host.local,disk0
,,0,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:46Z,15205102,io_time,diskio,host.local,disk0
,,0,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:56Z,15205226,io_time,diskio,host.local,disk0
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:26Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:36Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:46Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:56Z,648,io_time,diskio,host.local,disk2
`

	filteredDataRaw := `#datatype,string,long,dateTime:RFC3339,dateTime:RFC3339,dateTime:RFC3339,long,string,string,string,string
#group,false,false,true,true,false,false,false,false,true,true
#default,_result,,,,,,,,,
,result,table,_start,_stop,_time,_value,_field,_measurement,host,name
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:26Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:36Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:46Z,648,io_time,diskio,host.local,disk2
,,1,2017-10-10T00:00:00Z,2018-05-22T19:54:00Z,2018-05-22T19:53:56Z,648,io_time,diskio,host.local,disk2
`

	script := `import "csv"
data = "` + dataRaw + `"
csv.from(csv: data)
	|> range(start: 2017-10-10T00:00:00Z, stop: 2018-05-22T19:54:00Z)
	|> filter(fn: (r) => r._value < 1000)`

	wantFrom := getTablesFromRawOrFail(t, dataRaw)
	wantRange := getTablesFromRawOrFail(t, rangedDataRaw)
	wantFilter := getTablesFromRawOrFail(t, filteredDataRaw)

	ctx, deps := dependency.Inject(context.Background(), dependenciestest.Default())
	defer deps.Finish()
	vs, _, err := runtime.Eval(ctx, script)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("wrong number of side effect values, got %d", len(vs))
	}

	to, ok := vs[0].Value.(*flux.TableObject)
	if !ok {
		t.Fatalf("expected TableObject but instead got %T", vs[0].Value)
	}

	tos := flattenTableObjects(to, []*flux.TableObject{})

	fromCsvTO := tos[0]
	if fromCsvTO.Kind != csv.FromCSVKind {
		t.Fatalf("unexpected kind for fromCSV: %s", fromCsvTO.Kind)
	}
	rangeTO := tos[1]
	if rangeTO.Kind != universe.RangeKind {
		t.Fatalf("unexpected kind for range: %s", rangeTO.Kind)
	}
	filterTO := tos[2]
	if filterTO.Kind != universe.FilterKind {
		t.Fatalf("unexpected kind for filter: %s", filterTO.Kind)
	}

	compareTableObjectWithTables(t, fromCsvTO, wantFrom)
	compareTableObjectWithTables(t, rangeTO, wantRange)
	compareTableObjectWithTables(t, filterTO, wantFilter)
	// run it twice to ensure compilation is idempotent and there are no side-effects
	compareTableObjectWithTables(t, fromCsvTO, wantFrom)
	compareTableObjectWithTables(t, rangeTO, wantRange)
	compareTableObjectWithTables(t, filterTO, wantFilter)
}

func compareTableObjectWithTables(t *testing.T, to *flux.TableObject, want []*executetest.Table) {
	t.Helper()

	got := getTableObjectTablesOrFail(t, to)
	if !cmp.Equal(want, got) {
		t.Fatalf("unexpected result -want/+got\n\n%s\n\n", cmp.Diff(want, got))
	}
}

func getTablesFromRawOrFail(t *testing.T, rawData string) []*executetest.Table {
	t.Helper()

	b := bytes.NewReader([]byte(rawData))
	result, err := fcsv.NewResultDecoder(fcsv.ResultDecoderConfig{}).Decode(b)
	if err != nil {
		t.Fatal(err)
	}

	return getTablesFromResultOrFail(t, result)
}

func getTableObjectTablesOrFail(t *testing.T, to *flux.TableObject) []*executetest.Table {
	t.Helper()

	toc := lang.TableObjectCompiler{
		Tables: to,
	}

	program, err := toc.Compile(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	ctx, deps := dependency.Inject(context.Background(), executetest.NewTestExecuteDependencies())
	defer deps.Finish()

	q, err := program.Start(ctx, &memory.ResourceAllocator{})
	if err != nil {
		t.Fatal(err)
	}
	result := <-q.Results()
	if _, ok := <-q.Results(); ok {
		t.Fatalf("got more then one result for %s", to.Kind)
	}
	tables := getTablesFromResultOrFail(t, result)
	q.Done()
	if err := q.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func getTablesFromResultOrFail(t *testing.T, result flux.Result) []*executetest.Table {
	t.Helper()

	tables := make([]*executetest.Table, 0)
	if err := result.Tables().Do(func(table flux.Table) error {
		converted, err := executetest.ConvertTable(table)
		if err != nil {
			return err
		}
		tables = append(tables, converted)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	executetest.NormalizeTables(tables)
	return tables
}

func flattenTableObjects(to *flux.TableObject, arr []*flux.TableObject) []*flux.TableObject {
	for _, parent := range to.Parents {
		arr = flattenTableObjects(parent, arr)
	}
	return append(arr, to)
}

var crlfPattern = regexp.MustCompile(`\r?\n`)

func toCRLF(data string) string {
	return crlfPattern.ReplaceAllString(data, "\r\n")
}
