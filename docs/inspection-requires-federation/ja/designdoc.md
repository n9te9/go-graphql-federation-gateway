# Design Doc : @external + @requires Cross-Service 性能分析

## Background

ベンチマーク結果（Docker 内部 DNS 使用、公平な条件）:

```
Go Gateway:     1790.54 req/s  avg: 0.0279s
Apollo Router:  2014.40 req/s  avg: 0.0248s
差異: Apollo が約 1.12x 高速
```

ベンチマーククエリ（EC ドメイン）:

```graphql
query ProductWithShipping {
  product(id: "p1") {
    id name price
    reviews { id body authorName }
    inStock
    shippingCost          # @requires(fields: "weight")
  }
}
```

このクエリは **3 つのサービス** にまたがる federation クエリで、`shippingCost` は `@requires(fields: "weight")` により products サービスから取得した `weight` が必要になる。

## 実行計画の構造

```
Step 0: products サービス (root query)
  → product(id: "p1") { __typename id name price weight }
  ↓ (依存: なし)

Step 1: reviews サービス (entity step)       ┐
  → _entities([{ __typename, id }]) { reviews { ... } }   │ 並列実行
  ↓ (依存: Step 0)                            │ (DependsOn: [0])

Step 2: inventory サービス (entity step)     ┘
  → _entities([{ __typename, id, weight: 2.5 }]) { inStock shippingCost }
  ↓ (依存: Step 0)
```

Step 1 と Step 2 は両方 Step 0 にのみ依存するため、**実装上は並列実行される**（`findReadySteps` が両方を返し `executeSteps` が errgroup で並列実行）。

## ボトルネック分析

### 🔴 ボトルネック 1: HTTP クライアントのコネクションプール未設定（最大原因）

**場所**: `gateway/gateway.go:90-95`

```go
httpClient := &http.Client{
    Timeout: 3 * time.Second,
}
// Transport が設定されていない → http.DefaultTransport を使用
```

`http.DefaultTransport` のデフォルト値:

| パラメータ | デフォルト値 | 問題 |
|-----------|------------|------|
| `MaxIdleConnsPerHost` | **2** | concurrency 50 では 48 リクエストがコネクション待ちになる |
| `MaxConnsPerHost` | 0 (無制限) | 新規 TCP 接続を大量生成 |
| `IdleConnTimeout` | 90s | 問題なし |

**concurrency 50 で `MaxIdleConnsPerHost: 2` の影響**:

```
同時リクエスト 50 件:
  - 2 件: アイドル接続を再利用 ✓
  - 48 件: 新規 TCP 接続確立 or 接続キュー待ち
            → TCP ハンドシェイク オーバーヘッド (~0.1ms/接続)
            → 高スループット時は接続キュー待ちが支配的
```

**Apollo Router との比較**:
Apollo Router (Rust hyper) はデフォルトでホストごとに大きなコネクションプールを持ち、接続を効率的に再利用する。

---

### 🔴 ボトルネック 2: sendRequest のリクエストごとアロケーション

**場所**: `federation/executor/executor_v2.go:1207-1220`

```go
// 毎リクエスト map を生成してから json.Marshal
reqBody := map[string]interface{}{
    "query": query,
}
if len(variables) > 0 {
    reqBody["variables"] = variables
}
bodyBytes, err := json.Marshal(reqBody)
// ...
req, err := http.NewRequestWithContext(ctx, "POST", host, bytes.NewReader(bodyBytes))
```

1 回の `_entities` リクエストごとに発生するアロケーション:

| オブジェクト | 説明 |
|------------|------|
| `map[string]interface{}` | リクエストボディ用 map |
| `json.Marshal` 出力 | `[]byte` (ボディサイズ分) |
| `bytes.NewReader` | io.Reader ラッパー |
| `http.Request` | リクエスト本体 |
| `io.ReadAll` 出力 | レスポンスボディ `[]byte` |

concurrency 50 × 3 ステップ = **150 回/req の map + bytes アロケーション** が並列で発生する。

**Apollo Router との比較**:
Rust の hyper は構造化されたリクエストビルダーを使用し、ヒープアロケーションを最小化する。

---

### 🟡 ボトルネック 3: ExecutionContext の RWMutex 競合

**場所**: `federation/executor/executor_v2.go:71, 299-326`

```go
type ExecutionContext struct {
    results map[int]interface{}
    errors  []GraphQLError
    mu      sync.RWMutex          // 全アクセスを1つの mutex で保護
}
```

`findReadySteps()` (Step 0 完了ごとに呼ばれる):
```go
execCtx.mu.RLock()
defer execCtx.mu.RUnlock()
for _, step := range execCtx.plan.Steps {  // O(N) 全ステップ走査
    if _, exists := execCtx.results[step.ID]; exists { continue }
    // ...
}
```

`processStep()` のロック取得回数（1 ステップあたり）:

| 箇所 | ロック種別 |
|-----|-----------|
| `extractRepresentations` 内 | `RLock` (親ステップ結果参照) |
| Step 0 完了時の結果保存 | `Lock` |
| `mergeEntityResults` | `Lock` (全マージ期間) |
| `findReadySteps` 呼び出し | `RLock` (全ステップ走査) |

concurrency 50 での競合: **50 並列リクエスト × 各 6-8 回のロック取得** = 300-400 回/req のロック操作が並列に発生する。

---

### 🟡 ボトルネック 4: @requires フィールドの representation 構築オーバーヘッド

**場所**: `federation/executor/executor_v2.go:787-911`

```go
// Step 2 (inventory) の representation 構築:
// 1. collectRequiredFields でスキーマを走査して weight フィールドを特定
requiredFields := e.collectRequiredFields(step)

// 2. buildRepresentationFromNodes で weight の値を親結果から抽出
// entity data: { __typename: "Product", id: "p1", weight: 2.5, ... }
// repr: { __typename: "Product", id: "p1", weight: 2.5 }
```

`collectRequiredFields` の実装:
```go
// entity 定義から @requires フィールドを毎回スキャン（キャッシュなし）
for _, sel := range step.SelectionSet {
    field, ok := sel.(*ast.Field)
    if fieldMeta, ok := entityDef.Fields[field.Name.String()]; ok {
        // RequiredFields を収集
    }
}
```

**問題**: `collectRequiredFields` はリクエストごとに毎回実行され、プラン生成時にキャッシュされない。

---

### 🟡 ボトルネック 5: ExecutionContext プールのリセットコスト

**場所**: `federation/executor/executor_v2.go:88-96`

```go
// プール返却時の results map クリア
for k := range execCtx.results {
    delete(execCtx.results, k)  // 個別削除 → GC プレッシャー
}
```

3 ステップのクエリでは `execCtx.results` に 3 エントリが入る。削除より **新規 make の方が効率的なケース** がある（small map の場合）。

---

### 🟢 問題ではない点（懸念されるが実際には問題なし）

**Step 1 と Step 2 の並列実行**: 実装済み。`findReadySteps` が両方を返し `executeSteps` が errgroup で同時実行する。

**プランキャッシュ**: 実装済み。`PlannerV2.planCache (sync.Map)` でヒット時は Parse/Validate/Plan をスキップ。

**JSON ライブラリ**: `goccy/go-json` を使用しており、標準 `encoding/json` より高速。

---

## Apollo Router が速い理由の仮説

| 要素 | Go Gateway | Apollo Router |
|------|-----------|--------------|
| HTTP コネクションプール | `MaxIdleConnsPerHost: 2` (デフォルト) | hyper のプール (大) |
| リクエストアロケーション | map + []byte 毎回 | Rust の所有権モデルで最小化 |
| Mutex 競合 | `sync.RWMutex` (shared) | Rust の ownership (コンパイル時保証) |
| JSON 処理 | goccy/go-json (反射ベース) | serde_json (ゼロコスト抽象化) |
| GC 停止時間 | GC あり (~0.5ms/pause) | GC なし |

---

## 改善優先度

| 優先度 | 改善項目 | 期待改善 | 難易度 |
|--------|---------|---------|--------|
| 🔴 P1 | HTTP Transport 設定（MaxIdleConnsPerHost 調整） | ~15-20% | 低 |
| 🔴 P1 | sendRequest のリクエストボディバッファプール化 | ~5-10% | 低 |
| 🟡 P2 | ExecutionContext mutex の細粒度化 | ~5-10% | 中 |
| 🟡 P2 | collectRequiredFields のプラン時キャッシュ | ~3-5% | 中 |
| 🟢 P3 | ExecutionContext プールのリセット最適化 | ~1-2% | 低 |

---

## 定量的見積もり

現在のレイテンシ内訳（推定、avg 27.9ms @ concurrency 50）:

```
┌─────────────────────────────────────────────────┐
│ TCP 接続確立 (new conn × subgraphs)    ~3-5ms  │  ← P1 で改善
│ Step 0 実行 (products-v2)              ~8ms    │
│ Step 1+2 並列実行 (reviews + inventory) ~12ms  │
│ JSON marshal/unmarshal × 3 steps       ~1-2ms  │  ← P1 で改善
│ mutex/GC オーバーヘッド                ~1-2ms  │  ← P2 で改善
│ pruneResponse                          ~1ms    │
└─────────────────────────────────────────────────┘
合計 ~27-28ms
```

P1 対応後の期待値:

```
┌─────────────────────────────────────────────────┐
│ TCP 接続確立 (再利用 → ~0ms)           ~0ms    │
│ Step 0 実行 (products-v2)              ~8ms    │
│ Step 1+2 並列実行 (reviews + inventory) ~12ms  │
│ JSON marshal/unmarshal (バッファプール) ~0.5ms  │
│ mutex/GC オーバーヘッド                ~1-2ms  │
│ pruneResponse                          ~1ms    │
└─────────────────────────────────────────────────┘
合計 ~23-24ms → 約 2100 req/s 相当
```

## まとめ

Apollo Router との差異（~12% 差）の主な原因は:

1. **HTTP コネクションプール**: `MaxIdleConnsPerHost: 2` により concurrency 50 では大部分のリクエストが新規 TCP 接続を使用している（最大要因）
2. **リクエストごとのアロケーション**: sendRequest での map + bytes 生成が GC プレッシャーを生む
3. **言語差**: Go の GC パウズ、sync.RWMutex のオーバーヘッドは Rust の所有権モデルと比較して根本的に差がある（これは改善困難）

改善可能な差異（P1+P2 対応）: **~12% → ~3-5%** まで縮小できる見込み。残差は主に言語レベルの差異（GC、mutex）による。
