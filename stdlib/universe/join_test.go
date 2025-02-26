package universe_test

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/influxdata/flux"
	"github.com/influxdata/flux/execute"
	"github.com/influxdata/flux/execute/executetest"
	"github.com/influxdata/flux/plan"
	"github.com/influxdata/flux/querytest"
	"github.com/influxdata/flux/stdlib/influxdata/influxdb"
	"github.com/influxdata/flux/stdlib/universe"
)

func TestJoin_NewQuery(t *testing.T) {
	tests := []querytest.NewQueryTestCase{
		{
			Name: "basic two-way join",
			Raw: `
				a = from(bucket:"dbA") |> range(start:-1h)
				b = from(bucket:"dbB") |> range(start:-1h)
				join(tables:{a:a,b:b}, on:["host"])`,
			Want: &flux.Spec{
				Operations: []*flux.Operation{
					{
						ID: "from0",
						Spec: &influxdb.FromOpSpec{
							Bucket: influxdb.NameOrID{Name: "dbA"},
						},
					},
					{
						ID: "range1",
						Spec: &universe.RangeOpSpec{
							Start: flux.Time{
								Relative:   -1 * time.Hour,
								IsRelative: true,
							},
							Stop: flux.Time{
								IsRelative: true,
							},
							TimeColumn:  "_time",
							StartColumn: "_start",
							StopColumn:  "_stop",
						},
					},
					{
						ID: "from2",
						Spec: &influxdb.FromOpSpec{
							Bucket: influxdb.NameOrID{Name: "dbB"},
						},
					},
					{
						ID: "range3",
						Spec: &universe.RangeOpSpec{
							Start: flux.Time{
								Relative:   -1 * time.Hour,
								IsRelative: true,
							},
							Stop: flux.Time{
								IsRelative: true,
							},
							TimeColumn:  "_time",
							StartColumn: "_start",
							StopColumn:  "_stop",
						},
					},
					{
						ID: "join4",
						Spec: &universe.JoinOpSpec{
							On:         []string{"host"},
							TableNames: map[flux.OperationID]string{"range1": "a", "range3": "b"},
							Method:     "inner",
						},
					},
				},
				Edges: []flux.Edge{
					{Parent: "from0", Child: "range1"},
					{Parent: "from2", Child: "range3"},
					{Parent: "range1", Child: "join4"},
					{Parent: "range3", Child: "join4"},
				},
			},
		},
		{
			Name: "from with join with complex ast",
			Raw: `
				a = from(bucket:"flux") |> range(start:-1h)
				b = from(bucket:"flux") |> range(start:-1h)
				join(tables:{a:a,b:b}, on:["t1"])
			`,
			Want: &flux.Spec{
				Operations: []*flux.Operation{
					{
						ID: "from0",
						Spec: &influxdb.FromOpSpec{
							Bucket: influxdb.NameOrID{Name: "flux"},
						},
					},
					{
						ID: "range1",
						Spec: &universe.RangeOpSpec{
							Start: flux.Time{
								Relative:   -1 * time.Hour,
								IsRelative: true,
							},
							Stop: flux.Time{
								IsRelative: true,
							},
							TimeColumn:  "_time",
							StartColumn: "_start",
							StopColumn:  "_stop",
						},
					},
					{
						ID: "from2",
						Spec: &influxdb.FromOpSpec{
							Bucket: influxdb.NameOrID{Name: "flux"},
						},
					},
					{
						ID: "range3",
						Spec: &universe.RangeOpSpec{
							Start: flux.Time{
								Relative:   -1 * time.Hour,
								IsRelative: true,
							},
							Stop: flux.Time{
								IsRelative: true,
							},
							TimeColumn:  "_time",
							StartColumn: "_start",
							StopColumn:  "_stop",
						},
					},
					{
						ID: "join4",
						Spec: &universe.JoinOpSpec{
							On:         []string{"t1"},
							TableNames: map[flux.OperationID]string{"range1": "a", "range3": "b"},
							Method:     "inner",
						},
					},
				},
				Edges: []flux.Edge{
					{Parent: "from0", Child: "range1"},
					{Parent: "from2", Child: "range3"},
					{Parent: "range1", Child: "join4"},
					{Parent: "range3", Child: "join4"},
				},
			},
		},
		{
			Name: "no 'on' parameter",
			Raw: `
				a = from(bucket:"flux") |> range(start:-1h)
				b = from(bucket:"flux") |> range(start:-1h)
				join(tables:{a:a,b:b})
			`,
			WantErr: true,
		},
		{
			Name: "zero-length on list",
			Raw: `
				a = from(bucket:"flux") |> range(start:-1h)
				b = from(bucket:"flux") |> range(start:-1h)
				join(tables:{a:a,b:b}, on: [])
			`,
			WantErr: true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			querytest.NewQueryTestHelper(t, tc)
		})
	}
}

func TestJoinOperation_Marshaling(t *testing.T) {
	data := []byte(`{
		"id":"join",
		"kind":"join",
		"spec":{
			"on":["t1","t2"],
			"tableNames":{"sum1":"a","count3":"b"}
		}
	}`)
	op := &flux.Operation{
		ID: "join",
		Spec: &universe.JoinOpSpec{
			On:         []string{"t1", "t2"},
			TableNames: map[flux.OperationID]string{"sum1": "a", "count3": "b"},
		},
	}
	querytest.OperationMarshalingTestHelper(t, data, op)
}

func TestMergeJoin_Process(t *testing.T) {
	tableNames := []string{"a", "b"}

	testCases := []struct {
		name    string
		spec    *universe.MergeJoinProcedureSpec
		data0   []*executetest.Table // data from parent 0
		data1   []*executetest.Table // data from parent 1
		want    []*executetest.Table
		wantErr error // expected error
	}{
		{
			name: "simple inner",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0},
						{execute.Time(2), 2.0},
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0},
						{execute.Time(2), 20.0},
						{execute.Time(3), 30.0},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0},
						{execute.Time(2), 2.0, 20.0},
						{execute.Time(3), 3.0, 30.0},
					},
				},
			},
		},
		{
			name: "simple inner with ints",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TInt},
					},
					Data: [][]interface{}{
						{execute.Time(1), int64(1)},
						{execute.Time(2), int64(2)},
						{execute.Time(3), int64(3)},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TInt},
					},
					Data: [][]interface{}{
						{execute.Time(1), int64(10)},
						{execute.Time(2), int64(20)},
						{execute.Time(3), int64(30)},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TInt},
						{Label: "_value_b", Type: flux.TInt},
					},
					Data: [][]interface{}{
						{execute.Time(1), int64(1), int64(10)},
						{execute.Time(2), int64(2), int64(20)},
						{execute.Time(3), int64(3), int64(30)},
					},
				},
			},
		},
		{
			name: "inner with unsorted tables",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(2), 1.0},
						{execute.Time(1), 2.0},
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(3), 10.0},
						{execute.Time(2), 30.0},
						{execute.Time(1), 20.0},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 2.0, 20.0},
						{execute.Time(2), 1.0, 30.0},
						{execute.Time(3), 3.0, 10.0},
					},
				},
			},
		},
		{
			name: "inner with nulls in join columns",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{nil, 100.0},
						{execute.Time(1), 1.0},
						{execute.Time(2), 2.0},
						{nil, 200.0},
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0},
						{nil, 300.0},
						{execute.Time(2), 20.0},
						{execute.Time(3), 30.0},
						{nil, 400.0},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0},
						{execute.Time(2), 2.0, 20.0},
						{execute.Time(3), 3.0, 30.0},
					},
				},
			},
		},
		{
			name: "disjoint join and group columns with nulls",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{nil, 0.0, "foo"},
						{execute.Time(1), 1.0, "foo"},
						{execute.Time(2), 2.0, "foo"},
						{execute.Time(3), 3.0, "foo"},
						{execute.Time(4), nil, "foo"},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{nil, 0.5, nil},
						{execute.Time(1), 1.5, nil},
						{execute.Time(2), 2.5, nil},
						{execute.Time(3), 3.5, nil},
						{execute.Time(4), nil, nil},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{nil, 0.0, nil},
						{execute.Time(1), 10.0, nil},
						{execute.Time(2), 20.0, nil},
						{execute.Time(3), 30.0, nil},
						{execute.Time(4), nil, nil},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key_a", Type: flux.TString},
						{Label: "key_b", Type: flux.TString},
					},
					KeyCols: []string{"key_a", "key_b"},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "foo", nil},
						{execute.Time(2), 2.0, 20.0, "foo", nil},
						{execute.Time(3), 3.0, 30.0, "foo", nil},
						{execute.Time(4), nil, nil, "foo", nil},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key_a", Type: flux.TString},
						{Label: "key_b", Type: flux.TString},
					},
					KeyCols: []string{"key_a", "key_b"},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, 10.0, nil, nil},
						{execute.Time(2), 2.5, 20.0, nil, nil},
						{execute.Time(3), 3.5, 30.0, nil, nil},
						{execute.Time(4), nil, nil, nil, nil},
					},
				},
			},
		},
		{
			name: "inner with missing values",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0},
						{execute.Time(2), 2.0},
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0},
						{execute.Time(3), 30.0},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0},
						{execute.Time(3), 3.0, 30.0},
					},
				},
			},
		},
		{
			name: "inner with multiple matches",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0},
						{execute.Time(2), 2.0},
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0},
						{execute.Time(1), 10.1},
						{execute.Time(2), 20.0},
						{execute.Time(3), 30.0},
						{execute.Time(3), 30.1},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0},
						{execute.Time(1), 1.0, 10.1},
						{execute.Time(2), 2.0, 20.0},
						{execute.Time(3), 3.0, 30.0},
						{execute.Time(3), 3.0, 30.1},
					},
				},
			},
		},
		{
			name: "inner with common tags",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t1"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a"},
						{execute.Time(2), 2.0, "a"},
						{execute.Time(3), 3.0, "a"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a"},
						{execute.Time(2), 20.0, "a"},
						{execute.Time(3), 30.0, "a"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a"},
						{execute.Time(2), 2.0, 20.0, "a"},
						{execute.Time(3), 3.0, 30.0, "a"},
					},
				},
			},
		},
		{
			name: "inner with common tags and nulls",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t1"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a"},
						{execute.Time(2), 2.0, "a"},
						{execute.Time(3), 3.0, "a"},
					},
				},
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.1, nil},
						{execute.Time(2), 2.1, nil},
						{execute.Time(3), 3.1, nil},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a"},
						{execute.Time(2), 20.0, "a"},
						{execute.Time(3), 30.0, "a"},
					},
				},
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.1, nil},
						{execute.Time(2), 20.1, nil},
						{execute.Time(3), 30.1, nil},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a"},
						{execute.Time(2), 2.0, 20.0, "a"},
						{execute.Time(3), 3.0, 30.0, "a"},
					},
				},
			},
		},
		{
			name: "join with mismatched schemas",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "foo"},
						{execute.Time(2), 2.0, "foo"},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					KeyCols: []string{},
					Data: [][]interface{}{
						{execute.Time(1), 1.5},
						{execute.Time(2), 2.5},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "bar"},
						{execute.Time(2), 20.0, "bar"},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key_a", Type: flux.TString},
						{Label: "key_b", Type: flux.TString},
					},
					KeyCols: []string{"key_a", "key_b"},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "foo", "bar"},
						{execute.Time(2), 2.0, 20.0, "foo", "bar"},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, 10.0, "bar"},
						{execute.Time(2), 2.5, 20.0, "bar"},
					},
				},
			},
		},
		{
			name: "join with mismatched schemas with null in group key",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "foo"},
						{execute.Time(2), 2.0, "foo"},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					KeyCols: []string{},
					Data: [][]interface{}{
						{execute.Time(1), 1.5},
						{execute.Time(2), 2.5},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, nil},
						{execute.Time(2), 20.0, nil},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key_a", Type: flux.TString},
						{Label: "key_b", Type: flux.TString},
					},
					KeyCols: []string{"key_a", "key_b"},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "foo", nil},
						{execute.Time(2), 2.0, 20.0, "foo", nil},
					},
				},
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "key", Type: flux.TString},
					},
					KeyCols: []string{"key"},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, 10.0, nil},
						{execute.Time(2), 2.5, 20.0, nil},
					},
				},
			},
		},
		{
			name: "inner with extra attributes",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t1"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a"},
						{execute.Time(1), 1.5, "b"},
						{execute.Time(2), 2.0, "a"},
						{execute.Time(2), 2.5, "b"},
						{execute.Time(3), 3.0, "a"},
						{execute.Time(3), 3.5, "b"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a"},
						{execute.Time(1), 10.1, "b"},
						{execute.Time(2), 20.0, "a"},
						{execute.Time(2), 20.1, "b"},
						{execute.Time(3), 30.0, "a"},
						{execute.Time(3), 30.1, "b"},
					},
				},
			},
			want: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a"},
						{execute.Time(1), 1.5, 10.1, "b"},
						{execute.Time(2), 2.0, 20.0, "a"},
						{execute.Time(2), 2.5, 20.1, "b"},
						{execute.Time(3), 3.0, 30.0, "a"},
						{execute.Time(3), 3.5, 30.1, "b"},
					},
				},
			},
		},
		{
			name: "inner with tags and extra attributes",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t1", "t2"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a", "x"},
						{execute.Time(1), 1.5, "a", "y"},
						{execute.Time(2), 2.0, "a", "x"},
						{execute.Time(2), 2.5, "a", "y"},
						{execute.Time(3), 3.0, "a", "x"},
						{execute.Time(3), 3.5, "a", "y"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a", "x"},
						{execute.Time(1), 10.1, "a", "y"},
						{execute.Time(2), 20.0, "a", "x"},
						{execute.Time(2), 20.1, "a", "y"},
						{execute.Time(3), 30.0, "a", "x"},
						{execute.Time(3), 30.1, "a", "y"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a", "x"},
						{execute.Time(1), 1.5, 10.1, "a", "y"},
						{execute.Time(2), 2.0, 20.0, "a", "x"},
						{execute.Time(2), 2.5, 20.1, "a", "y"},
						{execute.Time(3), 3.0, 30.0, "a", "x"},
						{execute.Time(3), 3.5, 30.1, "a", "y"},
					},
				},
			},
		},
		{
			name: "inner with multiple values, tags and extra attributes",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t1", "t2"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a", "x"},
						{execute.Time(2), 2.0, "a", "x"},
						{execute.Time(2), 2.5, "a", "y"},
						{execute.Time(3), 3.5, "a", "y"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a", "x"},
						{execute.Time(1), 10.1, "a", "x"},
						{execute.Time(2), 20.0, "a", "x"},
						{execute.Time(2), 20.1, "a", "y"},
						{execute.Time(3), 30.0, "a", "y"},
						{execute.Time(3), 30.1, "a", "y"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a", "x"},
						{execute.Time(1), 1.0, 10.1, "a", "x"},
						{execute.Time(2), 2.0, 20.0, "a", "x"},
						{execute.Time(2), 2.5, 20.1, "a", "y"},
						{execute.Time(3), 3.5, 30.0, "a", "y"},
						{execute.Time(3), 3.5, 30.1, "a", "y"},
					},
				},
			},
		},
		{
			name: "inner with multiple tables in each stream",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0},
					},
				},
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0},
					},
				},
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0},
					},
				},
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0},
					},
				},
				{
					KeyCols: []string{"_value"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"_value_a", "_value_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 1.0},
					},
				},
				{
					KeyCols: []string{"_value_a", "_value_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0, 2.0},
					},
				},
				{
					KeyCols: []string{"_value_a", "_value_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0, 3.0},
					},
				},
			},
		},
		{
			name: "inner with multiple unsorted tables in each stream",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(3), "a"},
						{execute.Time(1), "a"},
					},
				},
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(4), "b"},
						{execute.Time(2), "b"},
					},
				},
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(5), "c"},
						{execute.Time(2), "c"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(8), "a"},
					},
				},
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(5), "b"},
						{execute.Time(7), "b"},
						{execute.Time(6), "b"},
					},
				},
				{
					KeyCols: []string{"_key"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_key", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), "c"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"_key_a", "_key_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_key_a", Type: flux.TString},
						{Label: "_key_b", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
					},
					Data: [][]interface{}{
						{"a", "c", execute.Time(1)},
					},
				},
				{
					KeyCols: []string{"_key_a", "_key_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_key_a", Type: flux.TString},
						{Label: "_key_b", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
					},
					Data: [][]interface{}{
						{"c", "b", execute.Time(5)},
					},
				},
			},
		},
		{
			name: "inner with different (but intersecting) group keys",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t2"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a", "x"},
						{execute.Time(2), 2.0, "a", "x"},
						{execute.Time(3), 3.0, "a", "x"},
					},
				},
				{
					KeyCols: []string{"t1", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, "a", "y"},
						{execute.Time(2), 2.5, "a", "y"},
						{execute.Time(3), 3.5, "a", "y"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a", "x"},
						{execute.Time(1), 10.1, "a", "y"},
						{execute.Time(2), 20.0, "a", "x"},
						{execute.Time(2), 20.1, "a", "y"},
						{execute.Time(3), 30.0, "a", "x"},
						{execute.Time(3), 30.1, "a", "y"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1_a", "t1_b", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1_a", Type: flux.TString},
						{Label: "t1_b", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a", "a", "x"},
						{execute.Time(2), 2.0, 20.0, "a", "a", "x"},
						{execute.Time(3), 3.0, 30.0, "a", "a", "x"},
					},
				},
				{
					KeyCols: []string{"t1_a", "t1_b", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1_a", Type: flux.TString},
						{Label: "t1_b", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, 10.1, "a", "a", "y"},
						{execute.Time(2), 2.5, 20.1, "a", "a", "y"},
						{execute.Time(3), 3.5, 30.1, "a", "a", "y"},
					},
				},
			},
		},
		{
			name: "inner with different (and not intersecting) group keys",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "t2"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a", "x"},
						{execute.Time(2), 2.0, "a", "x"},
						{execute.Time(3), 3.0, "a", "x"},
						{execute.Time(1), 1.5, "a", "y"},
						{execute.Time(2), 2.5, "a", "y"},
						{execute.Time(3), 3.5, "a", "y"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a", "x"},
						{execute.Time(2), 20.0, "a", "x"},
						{execute.Time(3), 30.0, "a", "x"},
					},
				},
				{
					KeyCols: []string{"t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.1, "a", "y"},
						{execute.Time(2), 20.1, "a", "y"},
						{execute.Time(3), 30.1, "a", "y"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1_a", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1_a", Type: flux.TString},
						{Label: "t1_b", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a", "a", "x"},
						{execute.Time(2), 2.0, 20.0, "a", "a", "x"},
						{execute.Time(3), 3.0, 30.0, "a", "a", "x"},
					},
				},
				{
					KeyCols: []string{"t1_a", "t2"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1_a", Type: flux.TString},
						{Label: "t1_b", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.5, 10.1, "a", "a", "y"},
						{execute.Time(2), 2.5, 20.1, "a", "a", "y"},
						{execute.Time(3), 3.5, 30.1, "a", "a", "y"},
					},
				},
			},
		},
		{
			name: "inner where join key does not intersect with group keys",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a", "x"},
						{execute.Time(2), 2.0, "a", "x"},
						{execute.Time(3), 3.0, "a", "x"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"t1"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "t1", Type: flux.TString},
						{Label: "t2", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 10.0, "a", "x"},
						{execute.Time(2), 20.0, "a", "x"},
						{execute.Time(3), 30.0, "a", "x"},
						{execute.Time(1), 10.1, "a", "y"},
						{execute.Time(2), 20.1, "a", "y"},
						{execute.Time(3), 30.1, "a", "y"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"t1_a", "t1_b"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "t1_a", Type: flux.TString},
						{Label: "t1_b", Type: flux.TString},
						{Label: "t2_a", Type: flux.TString},
						{Label: "t2_b", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 10.0, "a", "a", "x", "x"},
						{execute.Time(1), 1.0, 10.1, "a", "a", "x", "y"},
						{execute.Time(2), 2.0, 20.0, "a", "a", "x", "x"},
						{execute.Time(2), 2.0, 20.1, "a", "a", "x", "y"},
						{execute.Time(3), 3.0, 30.0, "a", "a", "x", "x"},
						{execute.Time(3), 3.0, 30.1, "a", "a", "x", "y"},
					},
				},
			},
		},
		{
			name: "inner satisfying eviction condition",
			spec: &universe.MergeJoinProcedureSpec{
				TableNames: tableNames,
				On:         []string{"_time", "tag"},
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0, "b"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0, "c"},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, "a"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0, "b"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0, "c"},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(1), 1.0, 1.0, "a"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(2), 2.0, 2.0, "b"},
					},
				},
				{
					KeyCols: []string{"tag"},
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value_a", Type: flux.TFloat},
						{Label: "_value_b", Type: flux.TFloat},
						{Label: "tag", Type: flux.TString},
					},
					Data: [][]interface{}{
						{execute.Time(3), 3.0, 3.0, "c"},
					},
				},
			},
		},
		{
			name: "two failures",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Err: errors.New("expected error"),
				},
			},
			data1: []*executetest.Table{
				{
					ColMeta: []flux.ColMeta{
						{Label: "_time", Type: flux.TTime},
						{Label: "_value", Type: flux.TFloat},
					},
					Err: errors.New("expected error"),
				},
			},
		},
		{
			name: "extra column",
			spec: &universe.MergeJoinProcedureSpec{
				On:         []string{"_time", "Alias", "Device", "SerialNumber"},
				TableNames: tableNames,
			},
			data0: []*executetest.Table{
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Pitch", Type: flux.TFloat},
						{Label: "Angle", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(1), "12345", execute.Time(1), 8.4, 1.2},
					},
				},
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Pitch", Type: flux.TFloat},
						{Label: "Angle", Type: flux.TFloat},
						{Label: "Gauge", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(2), "13579", execute.Time(1), 9.3, 5.6, 9.3},
					},
				},
			},
			data1: []*executetest.Table{
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Pitch", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(1), "12345", execute.Time(1), 8.4},
					},
				},
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Pitch", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(2), "13579", execute.Time(1), 9.3},
					},
				},
			},
			want: []*executetest.Table{
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Pitch_a", Type: flux.TFloat},
						{Label: "Pitch_b", Type: flux.TFloat},
						{Label: "Angle", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(1), "12345", execute.Time(1), 8.4, 8.4, 1.2},
					},
				},
				{
					KeyCols: []string{"Alias", "Device", "SerialNumber", "_time"},
					ColMeta: []flux.ColMeta{
						{Label: "Alias", Type: flux.TString},
						{Label: "Device", Type: flux.TInt},
						{Label: "SerialNumber", Type: flux.TString},
						{Label: "_time", Type: flux.TTime},
						{Label: "Gauge", Type: flux.TFloat},
						{Label: "Pitch_a", Type: flux.TFloat},
						{Label: "Pitch_b", Type: flux.TFloat},
						{Label: "Angle", Type: flux.TFloat},
					},
					Data: [][]interface{}{
						{"SIM-SAM-M169", int64(2), "13579", execute.Time(1), 9.3, 9.3, 9.3, 5.6},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			id0 := executetest.RandomDatasetID()
			id1 := executetest.RandomDatasetID()

			parents := []execute.DatasetID{
				execute.DatasetID(id0),
				execute.DatasetID(id1),
			}

			tableNames := make(map[execute.DatasetID]string, len(tc.spec.TableNames))
			for i, name := range tc.spec.TableNames {
				tableNames[parents[i]] = name
			}

			d := executetest.NewDataset(executetest.RandomDatasetID())
			c := universe.NewMergeJoinCache(executetest.UnlimitedAllocator, parents, tableNames, tc.spec.On)
			c.SetTriggerSpec(plan.DefaultTriggerSpec)
			jt := universe.NewMergeJoinTransformation(d, c, tc.spec, parents, tableNames)

			l := len(tc.data0)
			if len(tc.data1) > l {
				l = len(tc.data1)
			}
			var err error
			for i := 0; i < l; i++ {
				if i < len(tc.data0) {
					if err = jt.Process(parents[0], tc.data0[i]); err != nil {
						break
					}
				}
				if i < len(tc.data1) {
					if err = jt.Process(parents[1], tc.data1[i]); err != nil {
						break
					}
				}
			}
			jt.Finish(parents[0], err)
			jt.Finish(parents[1], err)

			got, err := executetest.TablesFromCache(c)
			if err != nil {
				if tc.wantErr == nil {
					t.Fatalf("got unexpected error: '%s'", err)
				} else if err.Error() != tc.wantErr.Error() {
					t.Fatalf("got unexpected error: wanted '%s', got '%s'", tc.wantErr, err)
				}
			} else if tc.wantErr != nil {
				t.Fatalf("expected error '%s', but got none", tc.wantErr)
			}

			executetest.NormalizeTables(got)
			executetest.NormalizeTables(tc.want)

			sort.Sort(executetest.SortedTables(got))
			sort.Sort(executetest.SortedTables(tc.want))

			if !cmp.Equal(tc.want, got) {
				t.Errorf("unexpected tables -want/+got\n%s", cmp.Diff(tc.want, got))
			}
		})
	}
}
