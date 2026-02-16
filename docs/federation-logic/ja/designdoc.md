# DesignDoc for Federation Logic

# Summary

GraphQL Federation のロジックを整理し、設計をする

# Author

 - N9tE9

# Architecture

アプリケーションアーキテクチャの構成は下記のC4図の構成になっている。

```mermaid
C4Context
    title go-graphql-federation-gateway application architecture

    Boundary(b0, "go-graphql-federation-gateway") {
        System(planner, "planner")
        System(graph, "graph")

        Boundary(b1, "executor") {
            System(qb, "query_builder")
            System(executor, "executor")
        }

        System(gw, "gateway")
    }

    Rel(planner, graph, "")
    Rel(executor, graph, "")
    Rel(executor, planner, "")
    Rel(executor, qb, "")
    Rel(gw, executor, "")
```

## graph

### SubGraph

サブグラフは、1つのサービスに紐づいているものである。
例えば、ec のドメインが、product, inventory, review, account の4つのサービスで構成されていた時、
各サービスに紐づくグラフをサブグラフとして定義する。
EC ドメインは、4つのサブグラフで構成される。
サブグラフは下記のプロパティを保持する。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| Name | string | product |
| Host | string | product.example.com |
| Schema | *ast.Schema | - |
| entities | map[string]Entity | - |

サブグラフに紐づく Entity を保持する構造体は下記のような設計になる。
Entity は、@key ディレクティブが付与された ObjectType を指す。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| Keys | []EntityKey | id |
| isExtension | bool | true |
| Fields | map[string]Field | - |

Keys を保持する EntityKey の設計は下記のようになる。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| FieldSet | string | id |
| Resolvable | bool | true |

Entity 内部で保持する Field の構造体は下記のような設計とする。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| Name | string | reviews |
| Type | *ast.Type | [Review] |
| Requires | []string | - |
| Provides | []string | - |
| isShareable | bool | true |

### SuperGraph

スーパーグラフは、複数サービスを集約したものである。
例えば、ec のドメインが、product, inventory, review, account の4つのサービスで構成されている場合、
それぞれのサービスを集約したグラフをスーパーグラフとして定義する。
スーパーグラフは下記のプロパティを保持する。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| SubGraphs | []SubGraph | - |
| Schema | *ast.Schema | - |
| Ownership | map[string][]SubGraph | "Product.id" -> [SubGraph(Product)], "Query.product" -> [SubGraph(Product)] |

## Step

ステップは、サービスへのリクエストの単位として考える。
実行時に必要な下記のようなフィールドを持つ

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| ID | int | 0 |
| SubGraph | SubGraph | - |
| StepType | iota(int) | 0 |
| ParentType | string | - |
| SelectionSet | *ast.SelectionSet | - |
| Path | []string | ["Query", "products", "id"] |
| DependsOn | []int | [0, 1] |

## Plan

Federation は、クエリからスーパーグラフ、サブグラフの依存解決からクエリの実行計画を組み立てる。
Plan は、下記のいくつかの構成要素で構成される。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| Steps | []Step | - |
| RootStepIndexes | []int | [0, 2] |


## Planner

Planner は、Plan メソッドによってクエリの実行計画を生成する。
Plan は、下記のいくつかの構成要素で構成される。

| プロパティ名 | データ型 | 値の例 |
| --- | --- | --- |
| SuperGraph | *SuperGraph | - |

# Usecases / Algorithms

## SubGraph を初期化する

ゲートウェイを初期化する時に、サブグラフを初期化する。

```mermaid
flowchart TD
    Start[NewSubGraph] --> Parse[Schema Parsing & AST取得]
    Parse --> InitStruct[SubGraph構造体の初期化]
    InitStruct --> LoopType{全ての型定義を走査}
    
    %% Type Loop
    LoopType -- 次の型あり --> CheckEntity{Entityか?}
    CheckEntity -- Yes --> ParseKeys[Keys / Resolvable の解析]
    CheckEntity -- No --> LoopField
    
    ParseKeys --> AppendEntity[entitiesマップへ追加]
    AppendEntity --> LoopField{全てのフィールドを走査}

    %% Field Loop
    LoopField -- 次のフィールドあり --> GetDirectives[ディレクティブの取得]
    GetDirectives --> MapFlags[Requires/Provides/Shareable<br>を解析しField構造体へセット]
    MapFlags --> LoopField
    
    %% Loop End handling
    LoopField -- 完了 --> LoopType
    LoopType -- 完了 --> Return[SubGraphを返す]
```

## スーパーグラフを初期化する

スーパーグラフ を初期化する。

```mermaid
flowchart TD
    Start([NewSuperGraph]) --> Comp[Schema Composition<br>全サブグラフのスキーマを合成]
    Comp --> Init[SuperGraph 構造体の初期化<br>Ownership マップの作成]
    
    Init --> LoopType{合成スキーマの<br>全型定義を走査}
    
    %% Type Loop
    LoopType -- 次の型 (TypeName) --> IsObject{Object Type か?}
    IsObject -- No (Scalar/Enum等) --> LoopType
    IsObject -- Yes --> LoopField{その型の<br>全フィールドを走査}

    %% Field Loop
    LoopField -- 次のフィールド (FieldName) --> LoopSub{全サブグラフを走査}
    
    %% SubGraph Loop (Ownership Check)
    LoopSub -- 次のサブグラフ --> CheckOwn{そのサブグラフは<br>このフィールドを<br>解決可能か？}
    
    CheckOwn -- Yes --> AddOwn[Ownershipマップに追加 key: TypeName.FieldName, val: append SubGraph]
    AddOwn --> LoopSub
    CheckOwn -- No --> LoopSub
    
    %% Loop End handling
    LoopSub -- 全サブグラフ完了 --> LoopField
    LoopField -- 全フィールド完了 --> LoopType
    LoopType -- 全型完了 --> End([SuperGraph を返す])
```

## クエリプランを構築する

クエリプランによって各ステップを構築し、グラフの依存関係を解決する。

```mermaid
flowchart TD
    Start([BuildPlan 開始]) --> Init[Plan 初期化<br>RootStepIndexes 作成]
    Init --> Parse[Query AST 解析<br>Root Field の取得]
    
    Parse --> GroupRoot[Root Field を<br>担当サブグラフごとにグルーピング]
    GroupRoot --> CreateRoot[Root Step 作成<br>Type: StepTypeQuery]
    CreateRoot --> Queue[処理待ちキューに<br>Root Step を追加]
    
    Queue --> CheckQueue{キューは空か？}
    
    %% Main Loop
    CheckQueue -- No (次がある) --> Pop["Step を取り出す (Current Step)"]
    Pop --> LoopField{Step内の<br>全フィールドを走査}
    
    %% Field Processing
    LoopField -- 次のフィールド (F) --> Lookup[Ownership マップ確認<br>F の担当サブグラフは？]
    Lookup --> Compare{Current Step の<br>サブグラフと同じ？}
    
    %% Case A: 同じサブグラフ (そのまま追加)
    Compare -- Yes --> Merge[Current Step の<br>SelectionSet に F を追加]
    Merge --> Recurse[F の子フィールドを<br>走査対象に追加]
    Recurse --> LoopField
    
    %% Case B: 異なるサブグラフ (境界越え)
    Compare -- No --> NewStep[新しい Step を作成 Type: StepTypeEntity Parent: Current Step]
    NewStep --> Dependency["依存関係解決 Current Step に @key (id等) を注入 2. DependsOn に Current ID を追加"]
    
    Dependency --> AddPlan[Plan.Steps に 新 Step を追加]
    AddPlan --> PushQueue[新 Step をキューに追加]
    PushQueue --> LoopField
    
    %% End
    CheckQueue -- Yes (完了) --> Opt["Plan 最適化(重複Stepの統合など)"]
    Opt --> End("[Plan 完成]")
```

