# ADR 0004 — 検証が何を実行し、その記録が何を保持するか

[English](../adr/0004-what-the-verify-record-keeps.md)

**Status:** accepted
**Date:** 2026-09-03
**修正対象:** `docs/DESIGN.ja.md` §10.5 の `harness.verify.v1` フィールド一覧、および §10.7 の P3 スコープ

## Context

P3 は L6 を追加する —— repository が既に宣言している検査を発見し、実行し、その出力を
1 つの形状に正規化し、P4a で「これは 3 回目だ」を答えられるだけ安定した fingerprint を
agent に手渡す。

実装の過程で、設計文書が未決にしていたか別の答えを書いていた判断が 5 つ発生した。
1 本の ADR にまとめたのは、それらが同時に、同じ前提から生まれたためである。

> 検証層が実行してよいのは repository が**既に宣言したもの**だけであり、保持してよいのは
> 後の読み手が**同じ失敗を再び認識する**ために必要なものだけである。

前半は、このプログラムが引き起こしてよい effect を縛る。後半は、コマンド行についての
ADR 0002 の判断が、別の入口から同じ扉に辿り着いたものである —— ツールの出力とは任意の
プログラム出力であり、その永続的な複製は秘密が集積する新しい場所になる。

**この判断が答えている原則:** YAGNI と KISS を、リポジトリではなく*環境*に適用したもの。
agent が学ぶべき表面はコマンド 1 つと出力形状 1 つであり、このプログラムが実行してよい
表面は閉じた一覧である。

## Decision

### 1. 発見は ladder であり、最も具体的な段だけが走る

設計は発見元を 4 つ挙げた（`run_quality_checks.sh` / Makefile / `package.json` / cargo）が、
repository が複数を持つ場合をどうするかは書いていない。答えは union ではなく ladder である。

1. 集約 quality スクリプト（`scripts/run_quality_checks.sh`, `run_quality_checks.sh`）
2. Makefile の検査 target
3. 言語 manifest —— `package.json` の scripts、`Cargo.toml`、`go.mod`

最初に一致した段が勝つ。複数を同時に集めるのは manifest 段だけである。多言語 repository の
`Cargo.toml` は、その `package.json` を覆っているとは本当に主張していないからである。

union にすると、よくある場合 —— 中身が Makefile を呼ぶだけの集約スクリプト —— で同じ
ツールが 2 度走り、同じ失敗が 2 度報告される。これは単に雑音が増えるという話ではない。
fingerprint が「そのプロジェクトが同じ検査を何通りの方法で宣言しているか」の関数になり、
**「同じ失敗が 3 回」が数えられる対象でなくなる**。recurrence 検出こそ P3 が存在する理由
なので、それを壊す形は選択肢ではない。

`go.mod` は設計の一覧に追加した。`Cargo.toml` と同じ段 —— toolchain が自らの動詞を宣言する
manifest —— であり、これが無ければこのツールは自分自身が開発されている repository を検査
できないままだった。

### 2. 実行してよい target 名は閉じている

`check` / `verify` / `lint` / `typecheck` / `test`。どの段からも、これ以外は無い。

発見は、このプログラムが誰かの working tree で*何を実行するか*を決める。`test` target の
隣に `deploy` target がある repository は珍しくない。`deploy` を興味深いと判断する検証層は、
観測していると称しながら不可逆な effect を起こすものに変わっている。この一覧は短く、
退屈で、拡張には ADR を要する。

### 3. prohairesis は severity を 1 つしか出さない。それでも階梯は残す

このプログラムが導出する finding はすべて `HIGH` である。schema は
`CRITICAL/HIGH/MEDIUM/LOW` を保ち、採点は security-checker の減点表をそのまま保つ。

単一 severity は手抜きではなく正直な答えである。この層が知っているのは「repository が
検査を宣言したこと」と「その検査が通らなかったこと」だけであり、未使用 import が失敗する
テストより悪いかどうかは知らない。写像を発明すれば、それはこの層が**正しさの定義を持つ**
ことであり、§5 が L6 に禁じている唯一のものである。

階梯を残すのは、*自分で* finding を格付けする供給元が同じ形状に書き込むからである。
scanner の `CRITICAL` を `HIGH` に畳めば、誰かが実際に下した判断を捨てることになる。

帰結として、prohairesis が唯一の供給元であるとき `total_score` は粗い —— finding 1 件で
90、4 件以上でカテゴリ上限の 40 減点。これは受け入れる。score は端末を読む人間のための
要約であり、exit code の根拠は `Failed()`（finding が 1 件でもあるか）であって、意図的に
閾値ではない。

### 4. 保存される記録は fingerprint と location を保持し、message は保持しない

session 内の実行は `~/.prohairesis/sessions/<id>/verify.jsonl` に追記されるが、各 finding の
`message` は削除され、`redacted: true` が立つ。agent が読む `--json` 出力は完全であり、
永続的な複製はそうではない。

これは `argv` についての ADR 0002 の判断を、同じ理由と同じ証拠で再度行ったものである。
このコードの最初の実運用 —— `~/signate/_template` に対する実行 —— が返した finding の
message は、**`token` という語を含むソースコードの行そのもの**だった。それを生んだ検査が
secret scanner だからである。secret scanner の finding は、構造上、その repository で最も
機微な文字列である。それを、session より長く生き、暗号化されておらず、agent が読める
永続ファイルに書けば、検証層はこのマシンで認証情報を探すのに最も適した場所になる。

message の redaction は、ADR 0002 がコマンド行について却下したのと同じ理由で却下した。
任意の出力のどこが秘密かを正規表現で判定する機構は、本プロジェクトが計測し置き換えよう
としている当のものであり、その取りこぼしは沈黙する。

残るのは recurrence が必要とするもの —— category、severity、location、そして
**message を捨てる前に**その message から計算された fingerprint。帰結は隠さず明示する。
**保存された記録の fingerprint は、その記録から再計算できない。** それは同一性であって、
隣に置かれたものの digest ではない。再ハッシュできないことをテストで固定してあるのは、
次の読み手がそれを「修正」しないためである。

### 5. 記録は、それを読む phase より先に書き始める

`verify.jsonl` を読むものはまだ無い。読むのは P4a である。

これは YAGNI と据わりが悪いので、前提ではなく理由を書いておく。比較対象となる履歴の無い
fingerprint は装飾である —— それが答える問い *「これは前回と同じ失敗か？」* は、2 回目の
実行が存在するまで指示対象を持たない。いま記録を書くことが、P4a を fixture ではなく実
session に対して構築できるようにする。代替案は、中心的な成果物を行使できない P3 を出荷
することである。

YAGNI が実際に禁じている側は、やっていない —— カテゴリ別 fingerprint は無く、attempt
フィールドも無く、`last_green` ポインタも無く、読み手も無い。それらは、どんな形が必要かに
ついて証拠を持つ phase のものである。

### 6. 注入する context は 1 行であり、予算はコード自身が強制する

`SessionStart` は `verify` と `undo` を名指す 1 行だけを出す。上限 200 バイトは、その行を
生成する関数の内部で強制される。

200 バイトは、設計の「50 token 未満」（§6.6）を、tokenizer を同梱せずに数えられる単位で
表現したものである（英語での保守的な近似として 1 token = 4 バイト）。実測は 162 バイト。

2 点は既定ではなく判断である。この行が `verify` を名指すのは発見が何かを見つけたときだけ
である。「ここには何も宣言されていません」と答えるコマンドを指させば、context を払って
無駄な tool call を買うことになる。そして bare な stdout ではなく runtime の名前付き
context フィールドとして出す。将来の変更で誤って診断メッセージがそこに印字されたとき、
それが agent への*指示*になってしまわないようにするためである。

この行に戦略の語が含まれないことをテストで固定してある。§5 は L7 が agent に「何を試せ」
と言うことを禁じている。注入経路はその規則を最も壊しやすい場所であり、善意の人物が 1 行
足せば壊れる距離にある。

## Consequences

**良い点**
- どの検査でも出力形状は 1 つ。agent が学ぶものは 1 つで済む。
- この層が実行するのは、慣習的な検査 target の閉じた一覧だけである。
- 検証の記録が秘密の集積地になり得ない。これは注意ではなくテストで固定されている。
- fingerprint が時刻・pid・一時ディレクトリ・実行時間を跨いで生き残る。P4a のすべての
  前提条件である。

**悪い点。ただし受け入れる**
- **ladder は段を取り違えうる。** `Makefile` に `test` target があり、`package.json` に
  別の `test` script がある repository では、Makefile 側しか走らない。`source` が宣言元の
  ファイルを名指すので可視ではあるが、これは細かい話ではなく実際の制約である。
- **保存された記録は再検証できない。** その fingerprint は、記録が含む何からも辿れない。
- **score は粗い。** prohairesis が唯一の供給元のとき `total_score` の取りうる値は少なく、
  rank A と失敗した実行は同時に成立しうる。
- **scrub は数値だけが違う 2 つの失敗を融合しうる。** 実行時間や hex 識別子を消すためである。
  誤りの向きは意図的に選んだ —— 過剰な融合は recurrence を過少に数えるだけだが、不足すれば
  recurrence は検出不能になる。この count の上に載るものは、それを**証拠**として提示しなければ
  ならず、決して自動で行動してはならない。
- **`verify` は repository が宣言したコマンドを実行する。** 読んでいない repository で
  `prohairesis verify` を打てば、その Makefile やスクリプトが走る。これは *agent* に対しては
  何も新しく与えない（agent は既に shell を持っている）が、clone 直後の repository で実行する
  人間にとっては実際の考慮事項であり、`docs/ENFORCEMENT-HONESTY.md` に明記した。

## Verification

- `tests/scenarios/p3-verify.sh` —— `deploy` target が一度も走らないこと。時刻と pid を
  含む実際の subprocess 出力を通して、同じ失敗が 2 回とも同じ fingerprint を保つこと。
  違う失敗は違う fingerprint になること。ツール不在は skip かつ exit 0 であること。
  何も宣言していない repository は失敗ではないこと。repository のファイルと git 表層が
  変わらないこと。注入される 1 行が予算内であり、検証対象が無い場所では黙ること。
  保存された記録が `redacted` を持ち、finding 本文を 1 バイトも含まないこと。
- `internal/verify/run_test.go` —— 秘密を含む finding が保存記録に生き残らないこと。
- `internal/verify/schema_test.go` —— golden record の round-trip、その score がコードの
  計算と一致すること、redacted な記録が再ハッシュできないこと。
- `internal/verify/discover_test.go` —— ladder、閉じた target 一覧、lockfile による
  package manager の決定。
- `internal/notice/notice_test.go` —— 予算、1 行であること、戦略を含まないこと。
