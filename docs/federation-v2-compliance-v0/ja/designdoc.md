# Design Doc: Federation v2 Core Compliance Upgrade

## 1. Summary

現在の `go-graphql-federation-gateway` は、Apollo Federation v2 の基本機能（`_entities` クエリ生成など）を有していますが、v2 の高度な構成機能である **Entity Ownership の正確な判定（`resolvable: false`）**、**依存フィールドの解決（`@requires`）**、**フェッチ最適化（`@provides`）**、および **Mutation 実行** が未実装または不完全です。

本ドキュメントでは、これらの機能を実装し、Federation v2 仕様への適合性を高めるためのアーキテクチャ変更と実装指針を定義します。

## 2. Background & Problem Statement

現状の評価レポートにより、以下の致命的な欠陥が特定されています。

1.  **Entity Owner の誤認**: `@key(resolvable: false)` を考慮せず、スタブ（参照用定義）を所有者とみなすため、実体のないサブグラフへクエリを投げてエラーになる。
2.  **依存関係の無視**: `@requires` で指定されたフィールドを事前に取得（Inject）しないため、外部データを必要とする Computed Field が解決できない。
3.  **非効率なフェッチ**: `@provides` を無視するため、同一サブグラフで解決可能なフィールドに対しても不要なネットワークコール（`_entities`）が発生する。
4.  **Mutation 不可**: `QueryBuilder` が常に `query` オペレーションを生成するため、更新系処理が機能しない。

## 3. Goals

1.  **正確な Entity Ownership の解決**
    * `@key(resolvable: false)` が付与されたサブグラフを Entity の Primary Owner として扱わない。
2.  **依存フィールドの解決（`@requires`）**
    * `@requires` フィールドを解析し、親ステップ（Upstream）の Selection Set に自動注入する。
3.  **フェッチの最適化（`@provides`）**
    * `@provides` されたフィールドについて、所有権マップをオーバーライドし、現在のサブグラフでの解決を優先する。
4.  **Mutation のサポート**
    * `query` / `mutation` / `subscription` の種別を正しく伝搬し、実行する。

## 4. Non-Goals

* **Gateway Managed Federation (IntrospectAndCompose)**: `_service` クエリによる動的なスキーマ取得機能の実装（現状の静的ファイル読み込みを維持）。
* **Query Plan Tracing**: Apollo Studio 互換の Trace 出力。
* **Advanced Networking**: タイムアウト、CORS、認証などのHTTPサーバー層の強化（別タスク）。

## 5. Architecture

### 5.1. Metadata-Enriched Ownership Map

現在の `OwnershipMap`（単なる `Field -> Subgraph` マッピング）を拡張し、Federation v2 のメタデータを保持する構造へ変更します。

```go
// 変更イメージ
type FieldMetadata struct {
    OwnerSubgraphs []string // 定義を持つサブグラフID
    Requirements   []string // @requires(fields: "...") のパース結果
    ProvidedFields []string // @provides(fields: "...") のパース結果
    IsExternal     bool     // @external フラグ
}

type EntityMetadata struct {
    ResolvableSubgraphs []string // @key(resolvable: true) なサブグラフ (Primary Owners)
    StubSubgraphs       []string // @key(resolvable: false) なサブグラフ
}
```

### 5.2. Multi-Pass Planner Strategy

Planner の処理フローを単一パスからマルチパスへ変更します。

```mermaid
flowchart TD
    Req[Client Request] --> QP[Query Parser]
    QP --> Plan[PlannerV2]
    
    subgraph Planning Process
        Plan -->|Pass 1| Logic1[Ownership Grouping & @provides Check]
        Logic1 -->|Pass 2| Logic2[Dependency Injection (@requires)]
        Logic2 -->|Pass 3| Logic3[Operation Type Propagation]
    end
    
    Logic3 --> Exec[ExecutorV2]
```

1.  **Pass 1: Grouping & Provides Check**: 基本的なクエリ分割を行う。この際、親フィールドの `@provides` 指示を確認し、外部フェッチを回避する。
2.  **Pass 2: Dependency Injection**: 各ステップが必要とする `@requires` フィールドを特定し、それを解決可能な「親ステップ」の Selection Set に強制追加（Inject）する。
3.  **Pass 3: Operation Setting**: Root Operation (Query/Mutation) を各ステップへ伝搬させる。

## 6. Implementation Details

### 6.1. `resolvable: false` 対応

**課題**: スタブ定義（`resolvable: false`）を持つサブグラフが Owner として選択されてしまう。

**解決策**:
`SuperGraphV2` 初期化時に `@key` ディレクティブを解析し、`Resolvable=false` の場合は `EntityOwners` リストから除外、または優先度を下げるロジックを追加する。

```go
// 擬似コード: SuperGraphV2.GetEntityOwnerSubGraph
func (s *SuperGraphV2) GetEntityOwnerSubGraph(typeName string) *SubGraph {
    owners := s.EntityOwners[typeName]
    for _, owner := range owners {
        // resolvable: false のサブグラフはスキップ
        if !owner.IsResolvable(typeName) {
            continue 
        }
        return owner
    }
    // Resolvableなオーナーがいない場合はエラーまたはFallback
    return nil 
}
```

### 6.2. `@requires` 対応 (Dependency Injection)

**課題**: `Shipping.cost` が `weight` を必要とする場合、Gateway は `weight` を事前に取得し、`Shipping` サブグラフへのリクエスト（`_entities`）に含める必要がある。

**解決策**:
Planner 内で DAG（有向非巡回グラフ）を構築した後、子ノードから親ノードへ依存関係を遡る処理を追加する。

1.  **Analyze**: 各 Plan Node の Selection Set を走査し、`@requires` を持つフィールドを特定。
2.  **Inject**: そのフィールド（例: `weight`）を解決できる親ノード（またはルート）の Selection Set に追加する。
3.  **Representations**: `QueryBuilder` が `_entities` クエリを生成する際、Inject されたフィールドの値を `representations` 変数に含める。

### 6.3. `@provides` 対応 (Optimization)

**課題**: サブグラフ A が `@provides(fields: "author { name }")` を宣言している場合、`author.name` はサブグラフ B（本来の所有者）へ取りに行く必要がない。

**解決策**:
`buildOwnershipMap` 構築時、または Planning 時に「提供可能コンテキスト」を評価する。

1.  フィールド解決時、現在のコンテキスト（親フィールドが解決されたサブグラフ）を確認。
2.  親フィールドのメタデータに `@provides` が含まれ、かつ対象フィールドがマッチする場合、`OwnershipMap` の結果を無視して「現在のサブグラフ」を解決者とする。

### 6.4. Mutation Support

**課題**: `QueryBuilder` が文字列結合で常に `query { ... }` を生成している。

**解決策**:
`PlanV2` 構造体に `OperationType` を追加し、Builder で動的に切り替える。

```go
// 変更前
query := fmt.Sprintf("query %s { ... }", operationName)

// 変更後
opType := "query"
if plan.OperationType != "" {
    opType = plan.OperationType
}
query := fmt.Sprintf("%s %s { ... }", opType, operationName)
```

## 7. Plan & Milestones

1.  **Refactor**: `SuperGraphV2` のメタデータ構造体の拡張（OwnershipMap のリファクタリング）。
2.  **Step 1**: `resolvable: false` のフィルタリング実装。
3.  **Step 2**: Mutation (`OperationType`) のサポート実装。
4.  **Step 3**: `@requires` の Injection ロジック実装（最難関）。
5.  **Step 4**: `@provides` による最適化実装。
6.  **Test**: Apollo Federation Tests (compatibility suite) を使用した回帰テスト。