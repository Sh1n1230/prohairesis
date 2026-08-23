# ADR 0002 — event log が何を保持し、何を拒むか

[English](../adr/0002-what-the-event-log-keeps.md)

**Status:** accepted
**Date:** 2026-08-23
**修正対象:** `docs/DESIGN.ja.md` §10.5 の `harness.event.v1` フィールド一覧、および §11 の P2 受け入れ条件

## Context

P2 は observability 層を追加する — 3 つの hook、session ごとの append-only log、
それを読む 2 つのコマンド。実装の過程で、設計文書が別の答えを既に書いていた判断が
4 つ発生した。1 本の ADR にまとめたのは、それらが同時に、同じ前提から生まれたためで
ある。前提とは **必要以上を保持する記録、あるいは見た以上を主張する記録は、小さく
正直な記録より悪い** ということ。そして分割すれば、どれも単独では反論する価値のない
大きさの文書が 4 つできるだけだからである。

## Decision

### 1. コマンド行はハッシュ化し、保存しない

設計の `harness.event.v1` は `action{kind, paths, argv, host}` を持っていた。実装した
schema に `argv` は無い。shell action が記録するのはプログラム名と、コマンド行全体の
SHA-256 だけである。

本プロジェクトが定義するどの指標も、コマンドに対して同じ問いしか立てない —— *これは
前と同じものか？* `recurrence_rate` に必要なのは同一性。`rediscovery_cost` に必要なの
も同一性。本文を必要とするものは 1 つも無い。本文を保持すれば、誰も尋ねていない問いに
答えることになり、しかもその答えは蓄積する —— shell コマンドに貼られたトークン、接続
文字列、急いでいて一度だけ打った認証情報が、永続的で、暗号化されておらず、agent が
読めるファイルの中に。

redaction は検討して却下した。コマンド行に正規表現をかけてどこが秘密かを判定する機構
は、本プロジェクトが既に計測し、置き換えようとしている当のものである —— このマシンで
既存の denylist は 14 コマンドを拒否し、うち 13 は無害だった。それを記録側で再現する
のはより悪い。redaction の取りこぼしは**沈黙する**からで、除去されなかったものを誰も
見ない。

### 2. 時間予算は設けない

設計は P2 の受け入れ条件を「hook overhead p50 < 20ms」としていた。この数字は文書中に
ちょうど 1 回だけ現れ、どこからも導出されていない。近くにある唯一の定量的アンカーは
見積もり —— 「shell 起動 30ms × 500 call」—— であり、それは runtime を Go で書く論拠
としては十分だが、閾値になる資格はない。

実測（プロセス外から、計測器を被計測プロセスの外に置いて）:

| 呼び出し | p50 | p90 | p99 |
|---|---|---|---|
| 記録のみ | 23 ms | 24 ms | 27 ms |
| 記録 + checkpoint | 58 ms | 60 ms | 62 ms |

P0 baseline の約 103 tool call/session に対して、記録のみ 23 ms は session あたり
約 2.4 秒。無停止区間の中央値 285 秒に対する比である。

条件は調整ではなく**削除**する。調整するとは、最初の数字と同程度に根拠のない
第二の数字を発明することだからである。`metrics` は分布を記録し、*recorded, not budgeted*
という語とともに表示する。実 session が十分に溜まれば、そこから閾値を導出できる ——
friction について P0 が踏んだのと同じ順序であり、比較対象の baseline が存在するのは
そのおかげである。

計測は 1 つの変更を正当化した。hook が 1 tool call あたり `git` を 8 回起動し、同じ
問いに二度答えていることが見えた。重複を外して p50 は 42 ms から 23 ms に半減した。
これは必要に先んじた最適化ではなく、二度やっていた仕事の削除である。

### 3. `boundary_class` と `decision` は v1 に入れない

設計の event は `boundary_class` と `decision{outcome, enforcement, reason}` を持って
いた。どちらも存在しない。boundary 分類も enforcement も P5 以前には存在しないからで
ある。

定義して常に null を書くのではなく、**省く**。常に空のフィールドは招待状であり、その
宛先は P5 の実装者である —— *ここに枠がある、埋めよ*。この層の唯一の規則は「記録し、
判断しない」であり、それを失う最も確実な方法は、判定の形をした空欄を記録の中に残して
おくことだ。schema はバージョン付きである。記録すべき決定が生まれたとき、v2 を切れば
よい。

`checkpoint_ref` は v1 に入れる。今答えられる問いだからである。それは undo の開始点を
指し、checkpoint を取った event だけでなく**すべての** event に書かれる。「ここまで
戻るにはどうするか」という問いは log のどの地点でも答えを持ち、それを明示するのは読み
手に推論させるより安い。

### 4. 登録は独立したコマンドで、project scope から始める

`install.sh` は settings ファイルに触れない。hook の登録は
`prohairesis hooks install` が行い、既定は project scope、`--scope user` は明示的に
求める。

installer の約束は「バイナリをどこかに置き、それ以外は何もしない」—— shell profile も
設定も触らない。agent の振る舞いを決めるファイルを install の副作用として書き換えるの
は、その約束を内側から破ることである。

project scope を既定にしたのは、試すことが取り消し可能であるべきだからだ。1 つの
repository、その持ち主が既に読んでいるファイル、削除すれば元通り。そのコストは実在
する —— 観測はその repository の縁で止まり、10 個中 1 個だけを黙ってカバーする event
log は、無いより悪い。沈黙が証拠に見えるからである。そのコストは隠さない: `doctor` は
どの scope が記録中かを報告し、`metrics` は自分の数字がどの repository をカバーして
いるかを併記する。

## Consequences

**Good.**
- log が秘密の集積地になりえない。しかもその性質は注意ではなく test で固定されている。
- 受け入れ条件のどの数字も、計測に紐付いている。
- P5 は「判定の形をした空欄」の無い schema を引き継ぐ。
- `hooks uninstall` が settings ファイルをバイト単位で復元する。試用が本当に無料である
  のはそのためである。

**Bad, and accepted.**
- digest は読み戻せない。「あれはどのコマンドだったか」を log だけから調べることは構造
  上不可能である。runtime 自身の transcript が引き続きその調べ先になる。
- 予算が無いので、hook が遅くなっても何も失敗しない。分布は全 event に記録されるので
  劣化は可視だが、見る人にしか見えない。
- project scope は既定で部分的なカバレッジを生み、部分的カバレッジは最も誤読を招く失敗
  形態である。したがって log を読むものはすべて、自分のカバレッジを明示することを義務
  づけた。

## Verification

- `tests/scenarios/p2-hook-never-blocks.sh` —— state ディレクトリを削除・書き込み不可・
  ファイルで置換し、ゴミを渡し、200KB の payload を渡し、repository の外で走らせても
  hook は exit 0。失われた観測は、直前に失敗した経路と失敗要因を共有しない durable な
  経路に到達する。
- `internal/adapter/claudecode/payload_test.go` —— コマンド行は event に残らず、同一
  コマンドは同一 digest を生む。
- `internal/adapter/claudecode/settings_test.go` —— install → uninstall で、他人の hook
  を含む現実的な settings ファイルがバイト一致で戻る。
- `internal/event/schema_test.go` —— golden record が Go の型を往復し、schema に
  `boundary_class`・`decision`・`enforcement`・`allowed` という名のフィールドが無い。
- `tools/hook-latency.py` —— 上記の数値。
