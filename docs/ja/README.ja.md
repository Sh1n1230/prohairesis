# prohairesis

[English](../../README.md)

自身のコンピュータ上でAIエージェントを実行するためのマシンレベル・インフラストラクチャ。

エージェントを安全に使用するための設定パックではありません。目指すのは「手綱（leash）」の正反対です。アクションによる危険で不可逆かつ検証不可能な**影響（effects）**をシステム側で吸収することにより、エージェントに**可能な限り多くのエージェンシー（主体性・決定権）**を与えます。

> 人間はエージェントをマイクロマネジメントしない。
> 人間はエージェントが行動できる環境を設計する。

まずは [`THESIS.ja.md`](THESIS.ja.md)（または [`THESIS.md`](../THESIS.md)）からお読みください。また、ここに書かれている内容を信頼する前に [`ENFORCEMENT-HONESTY.ja.md`](ENFORCEMENT-HONESTY.ja.md)（または [`ENFORCEMENT-HONESTY.md`](../ENFORCEMENT-HONESTY.md)）をご一読ください。

---

## 名前について

προαίρεσις — アリストテレスにおいては「熟慮に基づく選択」、エピクテトスにおいては**我々次第（ἐφ' ἡμῖν）である唯一のもの**としての選択の能力、そしてそれをそれ以外のものから区別する実践そのものを指します。本プロジェクトの目的はまさにそこにあります。harness はエージェントの prohairesis を守るために存在し、境界モデルはその区別をソフトウェアとして描いたものです。

「harness」はドキュメント全体を通じて**カテゴリを指す普通名詞**として維持されます。prohairesis は harness の一実装であり、出力するスキーマを `harness.*` 名前空間に置いているのは、別の実装がそれを採用できるようにするためです。

## ステータス

**現時点で動作するもの:** セッション、チェックポイント、`diff`、`undo`、`doctor`。そして hook を登録すれば、**自動チェックポイントと、エージェントが何をしたかの append-only な記録**。エージェントランタイム自体の巻き戻し機能ではカバーできない領域（シェル経由で編集されたファイルや、gitでは無視されているが失うわけにはいかないファイル）をカバーします。

hook の登録は独立した明示的な手順であり、まずは1つのリポジトリでの試用から始まります:

```sh
prohairesis hooks install                  # このリポジトリだけ
prohairesis hooks install --scope user     # 常用すると決めたら、マシン全体へ
prohairesis hooks uninstall                # 元の設定ファイルにバイト一致で戻す
```

ここで登録されるものは tool call を拒否できません。すべての hook は、失敗した経路も含めて、あらゆる場合に exit 0 で終わります —— これは意図ではなく `tests/scenarios/p2-hook-never-blocks.sh` で表明されています。

**未実装:** 検証契約（verification contract）、メタ状態レイヤー（meta-state layer）、ポリシーコンパイラ（policy compiler）、マシン上限（machine ceiling）。これらはこの順で以降のフェーズに入ります。

つまり現段階の prohairesis は、**作業を復旧可能に保ち、何が起きたかを記録する**ものです。何も判断しませんし、何も止められません。

## なぜ可逆性を最優先にするのか

パーミッション（権限確認）プロンプトとは、不可逆性に対する価格設定メカニズムです。現在エージェンシーが制限されているのは、影響を取り消すことができないからです。影響を回復可能（リカバリ可能）にすれば、人間が「今後表示しない」をクリックしたからではなく、自然とそのコスト（プロンプトを出す必要性）はゼロに下がるはずです。

そのため、最初に構築すべきは「プロンプトをオフにすることを正当化できるもの」となります。

## インストール

```sh
curl -fsSL https://raw.githubusercontent.com/Sh1n1230/prohairesis/main/install.sh | sh
```

インストーラはダウンロードしたアーカイブをリリースのチェックサムと照合し、一致しない場合は続行を拒否します。sudo は一切使わず、シェルの設定ファイルも書き換えません。

**実行する前に中身を読んでください。** これは形式的な注意ではありません。本プロジェクトは「検証していないものを信用すべきではない」と主張しており、自身のインストーラだけを例外にするのは筋が通らないからです。各リリースページには、手動での検証手順と署名済みのビルド来歴（build provenance）の確認方法も記載されています。

ソースからビルドする場合（開発用）:

```sh
git clone https://github.com/Sh1n1230/prohairesis.git prohairesis/main
cd prohairesis/main && go build -o bin/prohairesis ./cmd/prohairesis
```

## 使い方

```sh
prohairesis doctor             # 実際に何が有効で、何が有効でないかを報告
prohairesis hooks install      # このリポジトリのセッションを記録し、
                               # 頼まなくてもチェックポイントを取る
prohairesis session start      # セッションを開始し、現在のツリーの状態をキャプチャ
prohairesis protect add data   # 失うわけにはいかないgit無視パスを保護対象として宣言
prohairesis checkpoint --label "リファクタ前"
prohairesis diff               # 前回のチェックポイント以降の変更差分を表示
prohairesis undo --dry-run     # 復元で何が行われるかを確認（ドライラン）
prohairesis undo               # ツリーをチェックポイントの状態に復元
prohairesis session end        # セッションを終了

prohairesis report             # 1つのセッションで何が起きたか
prohairesis metrics            # 導入前のベースラインに対する現在の摩擦
```

手動で開始したセッションは、あなたが終了するまで開いたままです。エージェントの再起動は、あなたが戻れるようにしておきたかった地点を捨てる理由になりません。hook が自分のために開いたセッションは、それを使っていた最後のエージェントが去ったときに閉じます。

終了コードは全体で統一されています: `0` 正常（clean）、`1` ゲート失敗（gate failed）、`2` エラー（error）。

## 最も重要な不変条件（Invariant）

prohairesis がアクティブな状態であっても、`git log --all`、`git status`、`git stash list`、`git branch -a`、reflog、`HEAD`、`for-each-ref` は、prohairesis が一度も触れていないリポジトリと**バイト単位で同一（byte-identical）**の出力を生成しなければなりません。`.git` 内には何も書き込まれません。ユーザーのインデックス（index/staging）は決して読み取られず、書き込まれることもありません。

gitの状態を乱してしまうような可逆性レイヤーは、提供するエージェンシーよりも多くのエージェンシーを奪ってしまいます。これは単なる意図ではなく、`tests/scenarios/p1-destroy-and-restore.sh` で厳密に検証（assert）されています。最初の設計案（`refs/harness/` 配下にチェックポイントのrefを置く方式）は、このテストに合格しなかったため却下されました。詳細は [`0001-checkpoint-store-location.ja.md`](0001-checkpoint-store-location.ja.md)（または [`0001-checkpoint-store-location.md`](../adr/0001-checkpoint-store-location.md)）を参照してください。

event log が何を保持し、何を拒むか、そして設計文書にあった唯一の性能数値をなぜ調整ではなく削除したかは [`0002-what-the-event-log-keeps.ja.md`](0002-what-the-event-log-keeps.ja.md)（または [`0002-what-the-event-log-keeps.md`](../adr/0002-what-the-event-log-keeps.md)）にあります。

## 行わないこと

`prohairesis undo` はワーキングツリーを復元します。リモートへのpushの取り消し（un-push）、送信の取り消し（un-send）、公開の取り消し（un-publish）、課金の取り消し（un-charge）などは行いません。すでにマシンの外に出てしまった影響は別種の問題であり、本プロジェクトではこれを曖昧にせず明確に区別しています。

## テスト

```bash
bash tests/run.sh
```

## 計測

本プロジェクトは、保護対象そのものを損なってしまうリスクを孕んでいます。これに対する防御策は「善意」ではなく「計測」です。そのため、何かをインストールする**前**に、実際のセッションから摩擦のベースライン（friction baseline）を収集します:

```bash
tools/baseline.sh > baseline.json
```

hook が記録を始めた後は、`prohairesis metrics` が同じ指標を再計算し、そのベースラインと並べて表示します。両側とも同一のコードから出しており、まだ測れない指標は 0 ではなく理由付きの `n/a` として示されます。各指標が何を支持でき、何を支持できないかは [`docs/ja/METRICS.ja.md`](METRICS.ja.md) にまとめてあります。

開発マシンでの最初の実行では、手書きの正規表現による拒否リスト（denylist）フックが14個のコマンドを拒否したものの、**そのうち13個は害のないもの（偽陽性率 93%）**であることが判明しました。それは、シークレットファイルを ignore し始めるための `.gitignore` への追記をブロックし、そのファイルが追跡されていないことを確認する `git status` をブロックし、フック自身のソースを読む操作をブロックしていました。これらのコマンドはサニタイズされた上で、回帰コーパスとして `tests/golden/false-block-corpus.jsonl` に固定されています。本プロジェクトが提供するいかなるポリシーも、これより優れた結果を出し、かつそれを証明しなければなりません。

## コントリビューション

[CONTRIBUTING.ja.md](CONTRIBUTING.ja.md) を参照してください。`main` は保護されており、作業は `種類/作業内容` 形式のブランチで行い、CI が green である Pull Request 経由でのみ取り込まれます。

## 設計記録

[`DESIGN.ja.md`](../DESIGN.ja.md) が設計文書です。内容は最新に保たれており、決定が置き換えられた箇所にはその ADR へのリンクが併記されます。

## ライセンス

GPL-3.0。AI エージェントの安全性と自律性に関する知識と実装は、その上に構築されるものも含めて、オープンな形で蓄積されるべきだと考えるためです。
