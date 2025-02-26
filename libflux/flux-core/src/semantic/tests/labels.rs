use super::*;

use crate::semantic::Feature;

#[test]
fn labels_simple() {
    test_infer! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "fill" => "(<-tables: [{ A with B: C }], ?column: B, ?value: D) => [{ A with B: D }]
                where B: Label
                "
        ],
        src: r#"
            x = [{ a: 1 }] |> fill(column: "a", value: "x")
            y = [{ a: 1, b: ""}] |> fill(column: "b", value: 1.0)
            b = "b"
            z = [{ a: 1, b: ""}] |> fill(column: b, value: 1.0)
        "#,
        exp: map![
            "b" => "string",
            "x" => "[{ a: string }]",
            "y" => "[{ a: int, b: float }]",
            "z" => "[{ a: int, b: float }]",
        ],
    }
}

#[test]
fn labels_unbound() {
    test_infer! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "f" => "(<-tables: [{ A with B: C }], ?value: D) => [{ A with B: D }]
                where B: Label
                "
        ],
        src: r#"
            x = [{ a: 1, b: 2.0 }] |> f(value: "x")
        "#,
        exp: map![
            "x" => "[{A with a:int, B:string}] where B: Label",
        ],
    }
}

#[test]
fn labels_dynamic_string() {
    test_error_msg! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "fill" => "(<-tables: [{ A with B: C }], ?column: B, ?value: D) => [{ A with B: D }]
                where B: Label
                "
        ],
        src: r#"
            column = "" + "a"
            x = [{ a: 1 }] |> fill(column: column, value: "x")
        "#,
        expect: expect![[r#"
            error: string is not Label (argument column)
              ┌─ main:3:44
              │
            3 │             x = [{ a: 1 }] |> fill(column: column, value: "x")
              │                                            ^^^^^^

            error: string is not a label
              ┌─ main:3:31
              │
            3 │             x = [{ a: 1 }] |> fill(column: column, value: "x")
              │                               ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

        "#]],
    }
}

#[test]
fn undefined_field() {
    test_error_msg! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "fill" => "(<-tables: [{ A with B: C }], ?column: B, ?value: D) => [{ A with B: D }]
                where B: Label
                "
        ],
        src: r#"
            x = [{ b: 1 }] |> fill(column: "a", value: "x")
        "#,
        expect: expect![[r#"
            error: record is missing label a
              ┌─ main:2:31
              │
            2 │             x = [{ b: 1 }] |> fill(column: "a", value: "x")
              │                               ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^

        "#]],
    }
}

#[test]
fn merge_labels_to_string() {
    test_infer! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        src: r#"
            x = if 1 == 1 then "a" else "b"
            y = if 1 == 1 then "a" else "b" + "b"
            z = ["a", "b"]
        "#,
        exp: map![
            "x" => "string",
            "y" => "string",
            "z" => "[string]",
        ],
    }
}

#[test]
fn merge_labels_to_string_in_function() {
    test_infer! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "same" => "(x: A, y: A) => A"
        ],
        src: r#"
            x = same(x: "a", y: "b")
            y = same(x: ["a"], y: ["b"])
        "#,
        exp: map![
            "x" => "string",
            "y" => "[string]",
        ],
    }
}

#[test]
fn attempt_to_use_label_polymorphism_without_feature() {
    test_error_msg! {
        env: map![
            "columns" => "(table: A, ?column: C) => { C: string } where A: Record, C: Label",
        ],
        src: r#"
            x = columns(table: { a: 1, b: "b" }, column: "abc")
            y = x.abc
        "#,
        expect: expect![[r#"
            error: string is not Label (argument column)
              ┌─ main:2:58
              │
            2 │             x = columns(table: { a: 1, b: "b" }, column: "abc")
              │                                                          ^^^^^

            error: record is missing label abc
              ┌─ main:3:17
              │
            3 │             y = x.abc
              │                 ^

        "#]],
    }
}
#[test]
fn columns() {
    test_infer! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "stream" => "stream[{ a: int }]",
            "map" => "(<-tables: stream[A], fn: (r: A) => B) => stream[B]"
        ],
        imp: map![
            "experimental/universe" => package![
                "fill" => "(<-tables: stream[{A with C: B}], ?column: C, ?value: B, ?usePrevious: bool) => stream[{A with C: B}]
        where
        A: Record,
        C: Label",
                "columns" => "(<-tables: stream[A], ?column: C) => stream[{ C: string }] where A: Record, C: Label",
            ],
        ],
        src: r#"
            import "experimental/universe"

            x = stream
                |> universe.columns(column: "abc")
                |> map(fn: (r) => ({ x: r.abc }))
        "#,
        exp: map![
            "x" => "stream[{ x: string }]",
        ],
    }
}

#[test]
fn optional_label() {
    test_error_msg! {
        config: AnalyzerConfig{
            features: vec![Feature::LabelPolymorphism],
            ..AnalyzerConfig::default()
        },
        env: map![
            "columns" => "(table: A, ?column: C) => { C: string } where A: Record, C: Label",
        ],
        src: r#"
            x = columns(table: { a: 1, b: "b" })
            y = x.abc
        "#,
        // TODO This fails because `column` is not specified but it ought to provide a better error
        expect: expect![[r#"
            error: record is missing label abc
              ┌─ main:3:17
              │
            3 │             y = x.abc
              │                 ^

        "#]],
    }
}
