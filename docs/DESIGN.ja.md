> **この文書について**
> 本プロジェクトの設計記録であり、**生きた文書**として維持されます（実装に合わせて更新されます）。
> 承認済みの判断が後の検証で覆った箇所には、それを置き換えた ADR へのリンクを併記しています。
> 設計判断を変更する場合は、この文書を黙って書き換えるのではなく `docs/adr/` に ADR を追加してください。
>
> **実装進捗**: P0（baseline 計測・正直さの文書化）、P1（可逆性基盤）、P2（観測 + 計測）は完了。P3 以降は未着手。
> P2 の実装で本文の記述を 4 点上書きしています（`argv` の削除 / 時間予算の撤回 /
> `boundary_class`・`decision` の v1 除外 / hook 登録の scope）。理由は
> [ADR 0002](adr/0002-what-the-event-log-keeps.md)（[日本語版](ja/0002-what-the-event-log-keeps.ja.md)）にあり、
> 以下の該当箇所はそれに合わせて更新済みです。
> **製品名**: 本文中の「harness」は原則として*カテゴリを指す普通名詞*です。実装の名前は **prohairesis**、
> 状態ディレクトリは `~/.prohairesis`、スキーマ名前空間は移植可能な契約として `harness.*` を維持します。

---

# Universal Harness — machine-level AI Agent infrastructure 設計

## Context

`/Users/shin1230/Git/harness` はほぼ空（`.agents/skills/` ← `.claude/skills/` symlink の雛形のみ）。
一方このマシンには「AI Agent を安全に使う」断片が3箇所に散らばり、**いずれも「Agent に指示して制御する」実装**になっている：

| 場所 | 実態 | 問題 |
|---|---|---|
| `~/.claude/settings.json` | `Read()` deny list + `Run(git push)` deny + PreToolUse hook | **`Run(...)` はどの tool にもマッチしない。`git push` は今まで一度も守られていない** |
| `~/.claude/hooks/block-secrets.sh:18` | コマンド文字列の正規表現 denylist | `credentials` を含むだけで deny（`cat docs/credentials-design.md` が落ちる）。一方 quoting 次第で `cat $HOME/.env` は抜ける。**厳しすぎて同時に緩い＝agency だけ削っている** |
| `~/signate/_template` | Hard Rules・validate_submission.py・run_quality_checks.sh | project-level に閉じ、invariant が prompt への指示として書かれている |

目標はこれらを **「Agent が作用できる環境の側を制御する」machine-level infrastructure** に置き換えること。Claude Code は adapter #1 にすぎない。

```bash
git clone <this-repo> && cd harness && ./install
```

### 確定方針
- core runtime: **Go 単一バイナリ**（PreToolUse は毎 tool call 走る。shell 起動 30ms × 500 call = 15秒の純粋な agency 税）
- enforcement: **Reversibility 優先 + policy compile**。OS sandbox は「作る」のではなく「設定を生成する」
- OS: **macOS 優先・Linux 移植可能**（OS 依存は `platform/` backend にのみ隔離）
- 中核 primitive: Reversibility / Observability / Verification / **Meta-State** / Policy

---

## 0. 検証済みの事実（推測ではなく実測）

| 事実 | 根拠 | 設計への帰結 |
|---|---|---|
| Claude Code **2.1.241 は native sandbox を既に持つ**: `sandbox.filesystem` / `sandbox.network`(`strictAllowlist`, `deniedDomains`, `allowUnixSockets`) / `sandbox.credentials`(env mask, SigV4 再署名) / `autoAllowBashIfSandboxed` | binary 内文字列を実測（`strictAllowlist`×5, `deniedDomains`×8, `sigv4`×69, `sandbox-exec`×2） | **sandbox を作らない。設定を生成する compiler を作る。** credential broker は不要 |
| machine ceiling 機構が存在: `/Library/Application Support/ClaudeCode/managed-settings.json` + `allowManagedHooksOnly` / `allowManagedPermissionRulesOnly` / `disableBypassPermissionsMode` | 実測（`managed-settings.json`×23, `allowManagedHooksOnly`×17） | **machine vs repo の単調性は harness の発明ではなくベンダ提供の precedence。root 所有ファイルが trust anchor** |
| **その実ファイルは未作成** | `ls` で No such file | 天井は空いている。harness が正当に owner になれる |
| `/rewind` は存在するが **「Rewinding does not affect files edited manually or via bash.」** | binary 内の文字列を実測 | **可逆性 primitive は `/rewind` と重複しない。ベンダが文書化した穴そのものを埋める** |
| root FS は **APFS** | `diskutil info /` | `clonefile(2)` で untracked ファイルの snapshot がほぼ無料 |
| session transcript が `~/.claude/projects/<slug>/*.jsonl` に蓄積済み | 実測 | **friction baseline を、コードを1行も書く前に取得できる** |

→ **harness は security runtime ではない。compiler と、可逆性・観測・meta-state の substrate である。** 当初 MVP の約半分（sandbox 実装・credential broker）はここで消えた。

---

## 1. 用語の分離と Autonomy の分解

| 概念 | 定義 | 決めるもの | Harness の関係 |
|---|---|---|---|
| **Capability** | Agent が *物理的に実行しうる* 操作 | OS + 導入ツール + sandbox profile | 床を高く取る。境界でのみ絞る |
| **Agency** | Agent が *聞かずに判断・選択・変更してよい* 範囲 | policy と **prompt の不在** | **最大化する** |
| **Autonomy** | 人間の介入なしに連続稼働できる長さ | 下式から創発 | **各因子を引き上げる** |
| **Harness** | context/tools/permissions/verification/**memory**/feedback/rollback/observability | 本プロジェクト | 環境であって手綱ではない |

### 本プロジェクトの作業仮説（H1）— *証明済みの法則ではない*

```
H1:  Autonomy ≈ Agency × Verification × Persistent State × Recovery
```

**これは仮説であり、本プロジェクトの設計はこれに賭けている。** 定量的に検証された関係ではなく、フェーズ順序を決めるための作業モデルとして採用する。以下を明示する:

- **主張の内容**: 乗法であること。どれか1因子がゼロに近ければ Autonomy はゼロに近い。model capability を上げても他因子がゼロなら効かない。
- **何が H1 を反証するか**: Persistent State と Recovery を 0→1 にしても（P1〜P4 完了後）`mean_uninterrupted_run` が baseline から実質的に伸びない場合。その場合 H1 は誤りであり、Autonomy は主に model capability の関数だったことになる。→ **本プロジェクトは L1+L5 の可逆性・監査ツールに縮退すべき**であり、それは正当な結論であって失敗の隠蔽ではない。
- **H1 に依存しないもの**: 可逆性（P1）と観測（P2）は H1 が偽でも単体で有用（壊れても戻せる・何をしたか分かる）。**賭けているのは P3/P4 の優先順位であって、プロジェクトの存在ではない。**

H1 が正しいと仮定した場合の現状のマシン:

| 因子 | 現状（推定） | 律速か |
|---|---|---|
| Agency | 中（誤検知 denylist と permission prompt で削られている） | 部分的に |
| Verification | 低（repo ごとに存在するが Agent が発見・解釈できない） | Yes |
| **Persistent State** | **ほぼゼロ**（context window 内にしか存在しない） | **Yes・最大** |
| Recovery | **ほぼゼロ**（`/rewind` は bash/手動編集を戻さない） | Yes |

→ H1 の下では **Autonomy のボトルネックは Agency ではなく Persistent State と Recovery**。これが後述のフェーズ順序を決めている。**この推定自体が P0 の baseline 計測で検証対象になる。**

### 中心的な着想（2つ）

**(1) permission prompt は irreversibility の価格付け機構である。** agency が配給制なのは effect が回復不能だから。irreversibility を消せば価格は自動的にゼロになる。
→ 設計規則: *人間が目にする prompt はすべて harness に対するバグ報告である。*

**(2) 問題は meta-cognition 能力の不在ではなく、外部基盤の弱さである。**
```
task → attempt → failure → attempt → failure → "I cannot solve this"
```
現在の Agent は meta-cognition が *できない* のではない。**長時間の行為過程について、自己状態・時間状態・環境状態・失敗履歴を持続的に参照できる外部基盤が弱い**のである。Agent は時刻 T にはそれらを知っている。時刻 T+n には失っている。失っているのは能力ではなく参照先。

→ **したがって作るべきものは新しい巨大な AI システムではない。既存の Agent が「忘れているもの」を構造化して外部に置くこと、それだけ。**
→ 設計規則: *Harness は Agent の代わりに考えない。Agent が自分の状態を再構成するための persistent state を提供する。*

**この (2) の診断には、より深い形の候補がある。** 「参照先を失った」のではなく「参照先が時間指標を
帯びるための形式を初めから持たない」のではないか —— 第二の作業仮説 **H2**（§15）。H1 と同じく反証条件付きで
立てており、正しければ §14.4 の見積もり較正と陳腐化は独立の現象ではなく**一つの欠落の二つの症状**になる。

---

## 2. 三つの問いへの批判的回答

### Q1「AI を信用せずに大きな agency を与える環境は作れるか？」

**部分的に Yes。分割線は鋭く、明示すべき。**

**信用不要にできる**: 影響が自分の制御下に完全に収まり復元可能な effect —— workspace 内の書き込み、プロセス生成、push しない git 操作。ここでは安全性は substrate の性質であり、**Agent の従順さが入力に入らないので最大限敵対的な Agent に対しても成立する**。

**信用不要にできない残余3クラス**:
1. **外向き effect** — 送金・credential の実使用・送信・publish・prod 変更。un-send するローカル substrate は存在しない。取りうる手は (i) capability を与えない (ii) **service 層で可逆な代替物に変換する**（main への push → PR、send → draft、apply → plan、live key → test key）(iii) 人間ゲート。第4の選択肢は無く、あると主張する設計は嘘。
2. **Exfiltration** — secret を読んで outbound query に埋める行為は snapshot では検知も復元もできない。network 層（`strictAllowlist`）と credential 層（mask）でしか解けない。
3. **Harness 自身の control plane** — PreToolUse hook は Agent のプロセス内で呼ばれ、Agent は任意 shell を実行できる。敵対的 Agent は L2 を自明に回避する。**敵対的意図に耐えるのは L0（kernel）だけ**。

**正直な定式化**: *ローカルで contained かつ reversible な状態に対する大きな agency は、AI を信用せずに安全にできる。それ以外は capability を削るか、service 層で可逆化するか、信用するかのいずれか。*
contained-reversible は全 action の9割以上を占め、**現在の permission prompt のほぼ 100% を発生させているクラス**。ここから prompt を消すことが製品そのもの。

### Q2「Agent の制御」と「環境の制御」の違い

| | Agent を制御（prompt/rules/skills） | 環境を制御（policy/checkpoint/state） |
|---|---|---|
| **単位** | 決定（tool call ごと） | invariant / affordance（全 call に渡って成立） |
| **タイミング** | 事前。何が危険かを予測する必要がある | 事後許容。起きてから bound / undo できる |
| **失敗モード** | **静か。** 不遵守は遵守に見える | **騒がしい。** 拒否された syscall は位置付きの観測事象 |
| **モデル依存** | 世代交代で劣化。指示が読み替えられる | provider・世代を跨いで不変 |
| **合成** | ルールは衝突・蓄積・腐る（実例: `Run(git push)`） | 境界は交わる。単調で検査可能 |
| **agency コスト** | 高く拡散的。安全な action まで狭める | 境界に集中。境界の内側ではゼロ |

具体テスト: 「`rm -rf` するなと指示/hook する」 vs 「書き込み可能集合は workspace で、事前状態は復元可能」。前者は `python -c "shutil.rmtree(...)"`・subagent・Makefile・モデル更新で破れる。後者は **どう削除されたかを気にしない**。

**重要な系（Meta-State 層の位置づけ）**: 「環境の制御」は制限だけを意味しない。**memory / time / state / topology は環境が提供する *affordance* であり、instruction ではない。** 「過去の試行を覚えておけ」と prompt で指示するのは Agent の制御（確率的・context を食う・compaction で消える）。「過去の試行が問い合わせ可能な場所に存在する」のは環境の制御（決定的・context ゼロ・compaction を跨ぐ）。
→ **Meta-State Infrastructure は agency を減らす層ではなく、増やす層である。**

**設計規則（テスト可能）**: 環境的 invariant または affordance として表現できるものを prompt の指示として書いてはならない。

### Q3「Harness が agency を損なうならそれは設計失敗か？」→ **Yes。ゆえに予算化・計測する**

1. **既定非ブロック規則** — どのコンポーネントも既定で deny してはならない。deny には B4/B5 分類（§3）が必要。それ以外は最大でも *記録* のみ。`~/signate/_template` の PostToolUse hook が既に持つ `|| true` 規律をアーキテクチャ invariant に昇格。
2. **Friction 予算** — 全コンポーネントは friction コストと期待損失削減を宣言する。計測 friction が正当化を超えたら **調整ではなく削除**。
3. **回帰ゲート** — `harness metrics` が install 前 baseline と比較し、`interrupts_per_session` や `context_tax` が回復事象を伴わずに増えたら **harness の回帰**として `doctor` が fail する。

**最も警戒すべき drift**: 自然な劣化方向は「Agent が何かをするのを止める非常に洗練された仕組み」になること。防ぐ唯一の機構が metrics であり、**Phase 5 の nice-to-have ではなく Phase 2 で出荷する**。

---

## 3. Boundary model

「危険か」ではなく **「取り消せるか」×「どこまで届くか」×「気づけるか」** で分類する。

**3つの補正**（素朴な 2×2 に対して）:

1. **可逆性は二値ではなく (回復コスト × 検知遅延)。** 原理的に可逆でも3日気づかなければ運用上は不可逆。*practically reversible ⟺ `time_to_detect < time_to_irreversibility` かつ `recovery_cost < damage_cost`*。→ **observability は「良いレポート機能」ではなく可逆性の主張の前提条件。**
2. **containment には信頼度の異なる2機構がある。** *scope reduction*（kernel が届かなくする）は敵対的 Agent に耐える。*pre-imaging*（snapshot）は誤る Agent には耐えるが snapshot store を壊す Agent には耐えない。
3. **最重要クラスが抜けていた: harness-reflexive action。** `~/.claude/settings.json`・`.claude/hooks/`・`.git/hooks/`・`~/.zshrc`・`PATH`・**harness 自身の設定・event log・meta-state の derived 部分**。ローカルで contained かつ技術的に可逆なので素朴な 2×2 ではゼロ摩擦バケツに落ちるが、**他のすべてを可逆・観測可能にしている機構を静かに無効化する**。

### 境界クラス

| Class | 名前 | 定義 | 既定の扱い | 機構 |
|---|---|---|---|---|
| **B0** | Inert | workspace 内の読み取り、純計算 | ゼロ摩擦。個別ログもしない | なし |
| **B1** | Contained + scope-reduced | workspace 内であることが証明できる書き込み | ゼロ摩擦 | L0 `allowWrite` scope |
| **B2** | Contained + pre-imaged | 宣言済み precious な非versioned パスへの書き込み、workspace 内の一括削除 | **先に checkpoint してからゼロ摩擦** | L1 snapshot → L5 記録 |
| **B3** | Locally irreversible | snapshot 集合の外の非versioned データ破壊、`git clean -xdf` | **B2 に変換できればゼロ摩擦。できなければ B5 に昇格** | L1 が pre-image を試行 |
| **B4** | **Harness-reflexive** | agent/harness/shell 設定・hooks・PATH・credential store・**harness の event log と derived meta-state** の変更 | **L0 で deny。prompt ではない。** 人間用に `harness policy edit` 経路 | L0 `denyWrite` + L2 記録 |
| **B5** | Outward-irreversible | 金銭、実サービスへの credential 使用、publish、送信、prod 変更、リモート状態削除 | **唯一の真の escalation。** 人間ゲート、または可逆な代替物へ変換 | L2 gate + L0 network allowlist |
| **B6** | Exfiltration-shaped | 非 allowlist 先への egress、生 credential 保持中の egress | allowlist で deny。prompt ではない | L0 `strictAllowlist` + mask |

**設計上の帰結**:
- **B0–B2 は本当にゼロ摩擦であること（ログのノイズも含めて）。** 製品価値が実現する場所。
- **harness の本当の工学的仕事は B3 → B2 の変換。** 「containable にしてからゼロ摩擦」＝ L1 そのもの。
- **B4 と B6 は prompt ではなく deny。** 「shell rc を編集していい？」は疲労時に人間が誤答する問い。境界は交渉しない。
- **B4 が meta-state に効くことは意図的**: Agent は自分の attempt history を書き換えられない（§6 の derived/declared 分離）。
- **B5 は稀であるべき。** 目標 2 escalation/session 未満。頻発したら**クラスを緩めるのではなく可逆な代替物を探す**。この surrogate 変換が本プロジェクトで最も価値の高い設計作業。

---

## 4. 委譲表 — 何を harness が持たないか

| 関心事 | 担当 | harness の役割 | 理由 |
|---|---|---|---|
| プロセス/FS/network の閉じ込め | **CC native sandbox (seatbelt)** | 設定を生成するだけ | 既存・kernel 裏付け。hook で再実装は厳密に劣化 |
| credential mask・SigV4 再署名 | **`sandbox.credentials`** | 設定生成 + Keychain から供給 | 再署名 proxy は本当に難しい。clone するな |
| tracked file の履歴と rollback | **git** | repo 外の object store（[ADR 0001](adr/0001-checkpoint-store-location.md)） | git *が* tracked file の可逆性 substrate |
| tool 経由編集の undo | **CC `/rewind`** | **何もしない** | 文書化された穴（bash/手動編集）だけを埋める |
| 安価な file snapshot | **APFS `clonefile`** | 呼ぶだけ | ほぼ無料 |
| machine 天井 | **`managed-settings.json`** | 生成して所有 | root 所有＝正しい trust anchor |
| secret/CVE/config スキャン | **gitleaks/semgrep/osv-scanner/trivy** (`~/security-checker`) | verification の1カテゴリとして呼ぶ | 既に正規化されて動いている |
| 権威ある検証 | **CI** | **何もしない** | harness は *ローカル* 層。明示的な非目標 |
| 会話履歴 | **`~/.claude/projects/*.jsonl`** | metrics のために読むだけ | 二重の secret 保有物を作らない |
| project の意味的知識（規約・設計方針） | **CLAUDE.md / skills** | **何もしない** | meta-state は *episodic*（この session で何が起きたか）であって *semantic* ではない。混ぜない |
| **戦略・仮説・実装・次の一手** | **Agent** | `verify` / `undo` / `state` を渡して退く | Agent の代わりに計画する harness は micromanagement の変装 |
| Prompt injection 耐性 | Agent の system prompt | 何もしない | 貢献は失敗時の *被害の限定* |

**harness が持つしかないもの（他に誰も持たない）**:
1. 複数 provider に跨る policy 記述を1箇所に持つこと
2. bash 側・手動編集側の可逆性（ベンダが文書化した穴）
3. untracked-precious ファイルの保護
4. **agency friction の計測**（世界のどこでも誰も測っていない）
5. **正規化され Agent から呼べる verification contract**
6. **compaction と session 境界を跨いで生き残る、checkpoint と verification に結合された episodic meta-state**

---

## 5. アーキテクチャ層

```
 L7  Meta-State    goal / time / attempt / topology / self-state    agent が読み書き
 L6  Verification      「正しさの確かめ方」                              agent が呼ぶ
 L5  Observability     append-only event log, metrics                   受動
 ────────────────────────────────────────────────────────────── agent から見える継ぎ目
 L4  Adapters          Claude Code | custom loop | …                    provider ごと
 L3  Policy compile    単一の宣言 → native 成果物                        事前
 ────────────────────────────────────────────────────────────── trust boundary
 L2  Advisory control  PreToolUse 評価器                                in-process・回避可能
 L1  Reversibility     session checkpoint + restore                     out-of-process
 L0  Containment       seatbelt / CC sandbox                            kernel 強制
```

**L5→L6→L7 は同一 substrate の上に積み上がる**: L7 の大半は L5 の event ledger と L6 の正規化 verification 結果からの *導出* である（§6）。これが「Meta-State は大きな新機構ではない」ことの根拠。

各層の「持ってはいけないもの」（scope creep はここで死ぬ）:

- **L0**: *コマンド* の意味論を持ってはいけない。知るのは path・domain・syscall だけ。コマンド文字列マッチを始めた瞬間、kernel の衣を着た L2 になる。
- **L1**: ユーザーが気にする git 意味論を持ってはいけない。**harness の有無で `git log`/`status`/`stash list`/reflog の出力が1バイトも変わらない**。巨大データの snapshot を持ってはいけない（copy ではなく `denyWrite` で守る）。外向き effect を undo するふりをしてはいけない。
- **L2**: **強制の幻想**を持ってはいけない。全決定に `enforcement: advisory` を記録する。巨大な正規表現 denylist を持ってはいけない。
- **L3**: runtime 状態を持ってはいけない。compile は純粋・offline・content-hash 可能。**緩める合成をしてはいけない**（交わりのみ）。
- **L4**: policy の意味論を持ってはいけない。*可否を判断する adapter は policy を fork している*。完全であるふりをしてはいけない。
- **L5**: 絶対に何もブロックしてはいけない。既定で会話本文を持ってはいけない。
- **L6**: gate を持ってはいけない。exit code と構造化 JSON を返すだけ。「正しさ」の定義を持ってはいけない（repo が既に宣言しているものを発見して走らせる）。
- **L7**: **モデル・推論・要約・学習を一切持ってはいけない。** L7 は storage + 機械的導出 + 取得のみ。LLM 呼び出しゼロ、埋め込みゼロ、heuristic な「理解」ゼロ。**戦略・仮説・次の一手を持ってはいけない** —— 事実と、既に存在する recovery 選択肢の列挙だけ。`"you should try X"` を1行でも出力したら micromanagement に転落している。Agent が宣言した confidence を真として扱ってはいけないし、それで gate してはいけない。**semantic な project 知識（CLAUDE.md の領分）を持ってはいけない。**

**この一覧の一般形（YAGNI / KISS / DRY）**: 上の各項は「まだ来ていないものを作らない」
「手渡す表面を小さく保つ」「1 つの事実に 1 つの住所」を、層の語彙で個別に述べたものである。
一般形としての 3 原則は**このリポジトリの開発と、このリポジトリが agent に手渡す環境の両方**に
適用する（`CONTRIBUTING.md`）。後半が重要で、小さく綺麗なツールを書きながら、その先の誰かに
大きく複雑なものを差し出すことは容易に起こる。ただし原則は「似て見えるだけのもの」を畳んでよい
免罪符ではない。原則と理由が逆を向いたときは、その理由を ADR に書く。

**正準 action model**（provider 非依存性の実体）:
```
Action = { kind: read|write|delete|exec|network|agent_config,
           paths: [...], argv: [...], host: ..., actor: {adapter, session_id} }
```
policy はこの語彙で書かれ、Claude Code の存在を知らない。

---

## 6. L7: Meta-State Infrastructure

### 6.0 この層の野心を先に縛る

**作るもの**: 既存の Agent が忘れているものを、構造化して外部に置く基盤。
**作らないもの**: 新しい AI システム。Agent を賢くするもの。

**名称について**: この層は **Meta-State Infrastructure** と呼ぶ。「Meta-Cognition」ではない。
Agent に meta-cognition 能力は既にある —— 欠けているのは **state** であり、その参照先である。層に "cognition" と名付けると、認知そのものを供給する層であるかのように読め、§6.0 の非目標（要約・判定・提案）へ向かう圧力を生む。**名前が設計を守る。**

**スコープを決める唯一のテスト**:
> *時刻 T に Agent が既に知っていて、時刻 T+n には失っているものは何か。*
> **その集合が L7 のスコープであり、それ以外は一切スコープ外。**

「知っていたら有用だろう」は基準にならない。それは augmentation であり、AI システムの構築である。L7 は **externalization のみ**を行う。

**このテストは拡張されている。** 上の形は *loss*（知っていて失う）だけを捉え、*invalidation*
（持ち続けている信念が偽になる —— 並行して作業する人間や他 agent がファイルを変えた場合）を
捉えない。両方を含む形に広げた: [ADR 0003](adr/0003-invalidation-is-in-scope.md)、§14.4。
**externalization と augmentation の境界は動いていない。**

**この境界には、より鋭い言い直しの候補がある** —— *L7 は形式を供給し、質料を供給しない*（§15.2）。
loss と invalidation を選言なしに覆うが、H2 が未検証のため本文はまだ書き換えていない。ADR 0005 で決める。

この規律から出る **明示的な非目標**（どれも実装しない）:

| やらないこと | なぜ |
|---|---|
| 履歴の要約・圧縮（LLM による） | 新しい推論の追加。しかも lossy で、Agent が元々持っていた情報を harness の解釈で汚す |
| 過去 session への埋め込み検索 / RAG | Agent が持っていなかった情報の生成。別プロダクト |
| 仮説のスコアリング・有望度判定 | 判断の代行。Agent の agency を直接削る |
| 次の実験の提案・自動計画 | micromanagement の定義そのもの |
| workspace の学習モデル・予測 | 観測ではなくモデル。腐り、しかも間違える |
| 「insight」「lesson learned」の抽出 | harness が意味を作り始めた瞬間、それは Agent の思考の置換 |

**帰結として L7 の実装は驚くほど小さい。** derived state は L5 の event ledger と L6 の正規化 verification 結果からの機械的な射影（projection）であり、declared state は schema 付きの key-value スライスにすぎない。**推論を一切含まないから小さい。** これは制約ではなく、この層が正しく設計されていることの徴候。

もう一つの帰結: **declared state は Agent 自身の表現をそのまま保持する。** harness が独自の ontology に翻訳しない。schema は包むだけで、中身の文字列は Agent が書いたまま。翻訳した瞬間、それは harness の解釈になる。

### 6.1 中心的な設計分割 — derived と declared

これがこの層の agency を守る鍵。

| | **Derived state**（harness が導出） | **Declared state**（Agent だけが書く） |
|---|---|---|
| 出所 | event ledger(L5) + verification 結果(L6) + git | Agent の明示的な書き込み |
| 例 | attempt 回数・経過時間・失敗 fingerprint・成功 checkpoint・変更 resource・topology | current hypothesis / confidence / rejected hypotheses+理由 / known unknowns / next intended experiment |
| Agent が書けるか | **書けない（B4 で deny）** | 書ける。**任意** |
| 忘れられるか | **忘れられない。協力ゼロで機能する** | 忘れられる（それでよい） |
| harness の扱い | 真実として扱う | **Agent の主張として扱う。真偽を判定しない。gate しない** |

**この分割から出る不可侵の規則**: *derived state は Agent の協力がゼロでも完全に機能する。declared state は純粋に加算的である。*
Agent が hypothesis を1度も書かなくても、attempt history も recurrence 検出も topology も動く。**「meta-state を書け」という義務を Agent に課さない** —— 課した瞬間それは Agent の制御であり micromanagement。

### 6.2 Temporal State — 時間と試行の外部表現

L5 の ledger から導出する:
```
attempts[]        : {n, started, ended, changed_paths[], verify_result, checkpoint_ref, failure_fingerprint}
elapsed           : session 経過 / 現 attempt 経過
last_green        : 最後に verify が通った checkpoint_ref と時刻
recurrence        : {fingerprint, count, attempt_ns[], first_seen}
```

**attempt の境界**は Agent に宣言させない（義務化しない）。導出する: `harness verify` の呼び出し、または B2/B3 checkpoint を attempt 境界として扱う。Agent が `harness attempt` を明示的に打った場合はそれを優先する。

**same failure recurrence 検出**: verification 出力が既に正規化されている（`harness.verify.v1` = security-checker と同一形状）ので、finding から安定した fingerprint を作れる。生 stderr ではなく **正規化された `{category, severity, normalized_message, location}` から hash** する（flaky なタイムスタンプ・PID・パスを除去）。

閾値到達時、harness は **structured signal** を出す:
```json
{"signal":"failure_recurrence","fingerprint":"a3f…","count":3,
 "attempts":[1,2,3],"elapsed_since_first":"11m",
 "last_green_checkpoint":"refs/harness/sessions/…/7",
 "available_recovery":["undo_to_last_green","diff_since_last_green"],
 "declared_hypotheses_untested":2}
```
**この signal には次の戦略が含まれない。** 事実と、既に存在する recovery 選択肢の列挙のみ。strategy revision を促すのは signal の存在であって、signal の内容ではない。

### 6.3 Spatial State — environment topology

workspace を単なるファイル集合ではなく topology として表現する。ただし **scope creep の危険が最も高いのはここ**（放置すると CMDB になる）。

**規律: topology は *観測* であって *モデル* ではない。**
- 出所はすべて既存の流れ: git（repo・branch・remote・dirty 状態）、この session が触ったディレクトリ/ファイル（L5 ledger）、この session が起動したプロセス・bind した port（L5 ledger）、依存 manifest（存在すれば読む）、egress event に現れた外部 endpoint。
- **マシン全体を能動 scan しない。** session が触れた領域のみ。
- **キャッシュを真実として持たない。** 読むたびに再計算する。腐る第二の情報源を作らない。

提供する2ビュー:
```
harness topology            # session が観測した環境
harness topology --changed  # この session が変更した resource と、その影響が届きうる領域
```
目的は「AI に空間認知を与える」ことではなく **Agent が毎回ゼロから environment を再発見する必要を減らす** こと。効果は `rediscovery_cost` メトリクス（§8）で測る。

### 6.4 Self State — declared

schema 付きの、Agent 専用の書き込み領域:
```yaml
goal:              # 人間 or Agent が設定。session 跨ぎで継承
current_hypothesis:
confidence:        # 数値。harness は解釈も gate もしない
rejected:          # [{hypothesis, reason, evidence: verify_result_ref|attempt_n, at}]
known_unknowns:    []
next_experiment:
```

**`rejected` が最も価値が高い。** 「失敗した」ではなく **「この条件下でこの仮説は棄却された」** を記録することが、無限に同じ失敗を繰り返さないための唯一の構造。`reason` と `evidence`（verify 結果や attempt への参照）を必須にする —— 根拠のない棄却は棄却として保存しない。

### 6.5 Persistence と scope

ユーザー指定は「session 単位」。ただし **context compaction は session 内で起きる**し、**task は session を跨ぐ**。両方に効かせるため:

- meta-state の物理単位は **session**（`~/.prohairesis/sessions/<id>/state.json`）
- 各 session は `parent_session` と `task_id` の lineage を持つ
- 同一 repo で新 session が始まると、直前 session の `goal` / `rejected` / `known_unknowns` / `last_green` を **carry-over 候補として提示**する（自動 merge はしない。Agent が引き継ぐか捨てるかを決める＝agency）
- compaction は session 内の出来事なので、state はそのまま生き残る —— **これが本層の最大の実用価値**

### 6.6 Agent からの見え方（context tax を守る）

ここが §8 の 2% ハード上限と衝突しかねない箇所。解決策:

**meta-state は原則 pull（Agent が問い合わせる）であって push（注入）ではない。**
- SessionStart で注入するのは **ポインタ1行のみ**（「`harness state` に前回からの引き継ぎがある / `harness verify`・`harness undo` がある」）。目標 < 50 token。
- 唯一 push するのは **structured signal**（recurrence 検出・時間予算超過）で、閾値到達時のみ。signal も 2% 上限に計上され、超えるなら truncate。
- `harness state show` / `topology` / `verify` はすべて Agent が任意のタイミングで呼ぶ CLI。

**Agent が読まない可能性**は正直な限界として残る（§11）。強制はしない —— 強制は Agent の制御であり、この層の存在意義に反する。

---

## 7. machine-level と repository-level

### 論拠

`git clone` は **攻撃者が書いたデータのダウンロード**である。repo の `.claude/settings.json`・`.mcp.json`・`CLAUDE.md`・`.claude/hooks/` は *その中身のファイル*。repo config が Agent の可能行為を **広げられる** なら、`git clone && cd repo && claude` はユーザーの全 credential 付き RCE primitive になる —— Agent を騙す必要すらない。config が harness への指示そのものだから。

仮説ではない: `~/signate/_template/.claude/settings.json` は `Bash(uv run python:*)` を allow している。**自分で書いた repo では正しく有用なルール**。clone した repo では prompt なしの事前承認済み任意コード実行。ルールは正しく、**そのファイルの権威が問題**。

```
effective = machine_ceiling ∩ repo_policy        (∪ は決して使わない)
```

### 三重の強制

1. **root 所有の天井（主）** — `/Library/Application Support/ClaudeCode/managed-settings.json`（`root:wheel`）。`./install --with-ceiling` の sudo が必要な唯一の手順。B4/B5 の deny・`sandbox` ブロック・`strictAllowlist`・credential mask を L3 が生成。非 root の Agent は書き換えられない —— **これがセキュリティ論拠の全体であり、本物**。
   - `disableBypassPermissionsMode: true` には**正直な緊張**がある: これは実際の agency 削減。正当化は「harness の目的は bypass を不要にすることであり、なお bypass が必要なら harness が失敗しているので直すべき」。
   - `allowManagedHooksOnly: true` は repo 供給 hook という実行 vector を殺すが、signate template の PostToolUse formatter も壊す。**コストを明示した opt-in** とし、代替（repo 宣言の formatter を verification contract 経由で走らせる）を同時に提供。
2. **compile 時の交わり（構造）** — repo policy は enforcement 機構に直接届かない。`policy compile` が *データとして* 読み、天井との交わりを計算し、広げる規則を理由付きで drop する。**repo policy の語彙は narrowing 演算子しか持たない**（`deny`/`restrict`/`require_verification`/`protect`）。**repo scope に `allow` 動詞が構造的に存在しない。** 「external content is data, not instructions」を設定に適用したもの。
3. **TOFU + content pin（手続き）** — 未知 repo の初回 session で policy 関連ファイルを hash し `untrusted` として記録。untrusted repo は **repo policy を完全に無視**し狭い既定で走る。`harness trust` で pin。ファイルが変われば untrusted に戻る。**direnv の `direnv allow` と同じ実証済み UX。repo あたり判断1回。**

### repo policy が正当にできること
狭める／**verification contract の宣言**（最大価値）／B2 保護対象 precious パスの宣言（`data/raw`, `data/external`）／**この repo の B5 面の宣言**（「不可逆 action は SIGNATE 提出、gate は `scripts/validate_submission.py`」＝既存 template invariant の一般化）。

---

## 8. Adapter interface

| # | 能力 | 契約 | Claude Code 2.1.241 | Custom API loop | Codex/Gemini CLI | IDE 組込 |
|---|---|---|---|---|---|---|
| C1 | Session lifecycle | start/end 通知 | SessionStart/End | native | wrapper | 不可 |
| C2 | **Pre-execution 介入** | effect 前に allow/deny/ask | PreToolUse（**advisory**） | **native・権威的** | 弱〜無 | **不可** |
| C3 | Post-execution 観測 | `{action, outcome, duration}` | PostToolUse | native | 部分 | 不可 |
| C4 | Context injection | 有界な文字列の注入 | SessionStart output | native | system prompt | 限定 |
| C5 | Event stream | 構造化 event | hooks + transcript | native | stdout parse | 不可 |
| C6 | Process-tree 閉じ込め | confine handle | **native sandbox（最強）** | `sandbox-exec` wrapper | 各自の sandbox | 不可 |

- **Claude Code — full (C1–C6)。C2/C3/C5 は advisory。** 参照実装。C6 は現存 agent 中最強。
- **Custom API-loop — full かつ権威的。** C2 が本物の gate になる唯一のケース。adapter #2 をここに作る価値は **継ぎ目が実在することの証明**にある。
- **Codex/Gemini CLI — 部分。** L0+L1+L5+L7 に degrade（sandbox・可逆性・観測・meta-state）。**L7 は C1+C5 だけで成立するので、ここでも効く** —— 価値の大部分は残る。
- **IDE 組込 (Cursor/Copilot 等) — interface を満たせない。** 安定した pre-execution 継ぎ目も confine 可能なプロセス木も session identity も無い。**正直に言う: L1 のみ提供**（FS watcher 駆動 checkpoint で `undo` が効くだけ）。脆い extension shim は作らない。

**adapter は grade を宣言し、`doctor` がどの層が無効かを表示する。静かな degrade こそが harness を「安心の錯覚」に変える失敗モード。**

---

## 9. Agency friction メトリクス

Q3 はこれ無しでは反証不能。ゆえに Phase 2 で出荷する。出所は `~/.claude/projects/*.jsonl`（既存）＋ harness event log。

| メトリクス | 定義 | 方向 | baseline |
|---|---|---|---|
| `interrupts_per_session` | 人間の決定点（prompt + ask + Agent の質問）数 | ↓ | **install 前に既存 transcript から取得可能** |
| `false_block_rate` | deny 後 N ターン以内に人間が同等 action を承認 / policy を編集した割合 | ↓ **目標 <5%** | 新規 |
| `context_tax` | harness が注入した token（絶対値と session 入力比） | ↓ **上限 2%（ハード）** | 0 |
| `mean_uninterrupted_run` | 人間介入間の実時間と tool call 数 | ↑ **autonomy の主指標** | 取得可能 |
| `recovery_events` | `undo` 実行と実際に復元された checkpoint | 初期↑（L1 が効いている証明）→低位安定 | 新規 |
| `escalation_precision` | B5 escalation のうち人間が承認した割合 | **0.7〜0.9 が健全。1.0 に近い＝クラスが広すぎ、prompt が儀式化** | 新規 |
| **`strategy_revision_rate`** | recurrence signal 発火後の次 attempt が **実質的に異なるか**（変更パス集合の差 / failure fingerprint の変化 / 根拠付き `rejected` の記録 のいずれか） | ↑ **L7 の直接指標** | signal 抑制条件との対照で取得 |
| `recurrence_rate` | 直前と同一 failure fingerprint を持つ attempt の割合 | ↓ | transcript から近似取得可能 |
| `rediscovery_cost` | 既に topology に記録済みの領域を再探索した tool call 数 | ↓ | 新規 |
| `resignation_rate` | verify green にも到達せず、根拠付き `rejected` の記録も無いまま終わった session の割合 | ↓ | 近似取得可能 |
| **`explanation_cost`** | 人間が Agent にコンテキストを説明するために払った量（session あたりの人間発話 token、および同一事実の再説明回数） | ↓ | **取得可能**（transcript から）。§14.5 |
| **`estimate_ratio`** | declared な工数見積もり ÷ verify green までの実測。**方向は ↓ ではなく →1.0** | →1.0 | 新規。実測側は既存。§14.4 |

### 9.1 「AI はすぐ諦める」をどう測るか — 単一指標に依存しない

**諦念は直接観測できない。** `resignation_rate` は *諦念の近似指標* にすぎず、単独では信用できない: 正当に「これは解けない」と結論した session と、状態を失って投げ出した session を区別できないし、人間が途中で介入して終わった session も混入する。単一の代理指標を最適化目標に据えると、**指標を満たすが本質を外した設計**（たとえば「諦めるな」と prompt で指示する = Agent の制御）に流れる。

代わりに **L7 の効果は因果的に直接測る**:

```
recurrence 検出  →  signal 発火  →  次 attempt で strategy revision が起きたか
                                     ↑ ここが L7 の効果そのもの
```

`strategy_revision_rate` が直接指標である理由: **L7 が提供したもの（signal）と、期待される効果（戦略変更）が、1ステップで因果的に結ばれている。** 代理も推定も挟まない。「同じ失敗を繰り返している」という事実を外部化したことが実際に行動を変えたか —— それが層の存在理由そのもの。

対照条件は P4a の実装で確保する: **signal を確率的に抑制した session を対照群として走らせ、抑制あり/なしで `strategy_revision_rate` を比較する**（paired-session A/B）。時系列 baseline より感度が高く、§12 で挙げた「session 間分散が信号を埋める」不確実性への回答にもなる。

指標族による三角測量（どれか1つでは結論しない）:

| 問い | 指標 | 種別 |
|---|---|---|
| L7 の signal は行動を変えたか | `strategy_revision_rate` | **直接・因果的** |
| 同じ失敗の反復は減ったか | `recurrence_rate` | 直接・結果側 |
| 環境の再発見コストは減ったか | `rediscovery_cost` | 直接・別経路 |
| 諦めて終わる session は減ったか | `resignation_rate` | **近似。単独では使わない** |
| 無停止で走る長さは伸びたか | `mean_uninterrupted_run` | H1 の検証指標 |

**`resignation_rate` が下がっても他が動かなければ L7 は効いていない**と判断する。逆に `strategy_revision_rate` が有意に上がっていれば、`resignation_rate` が動かなくても L7 は機能している（諦めの理由が他にあるだけ）。

### 9.2 合成指標

- **Friction index** = `interrupts_per_session × (1 + false_block_rate)`（baseline 正規化）。`harness metrics` の見出し数値。
- **Agency delta** = `mean_uninterrupted_run` の post/pre 比。**Policy phase 完了時点でこれが ≤1.0 なら、本プロジェクトは自らの thesis に失敗しており L1+L5+L7 のみに縮退すべき。**
- **H1 検証** = P4 完了時点の `mean_uninterrupted_run` が baseline から実質的に伸びていない場合、§1 の H1 は反証されたものとして扱い、**L1+L5 に縮退する**。この判定は自動で `harness metrics --h1` が出す。

---

## 10. MVP

### 10.1 primitive の優先順位

順序の原理: **`Autonomy ≈ Agency × Verification × Persistent State × Recovery` の、現在ゼロに近い因子から埋める。** そして **agency を増やす primitive を先に出荷する** —— 途中で頓挫しても環境が今日より厳密に良い状態で止まるように。

| # | primitive | 因子 | 判定 |
|---|---|---|---|
| 1 | **Reversibility** (session/checkpoint/undo) | Recovery: 0→1 | 「Rewinding does not affect files edited manually or via bash」で穴が *確認された*。prompt を切る前提条件 |
| 2 | **Observability** (event ledger/metrics) | 計測 + L7 の土台 | (i) 検知できない effect は不可逆なので **可逆性の主張の前提条件**、(ii) Q3 を反証可能にする、(iii) **L7 の derived state はすべてここから導出される** |
| 3 | **Verification contract** | Verification: 低→高 | agency と autonomy を両方増やす唯一の primitive。read-only でリスクゼロ、両参照資産に既にパターンあり、安い。**L7 の failure fingerprint もここに依存** |
| 4 | **Meta-State (L7)** | Persistent State: 0→1 | **最大の律速因子。** かつ #2/#3 の上に載る *導出* が大半なので新規機構は小さい。derived 先・declared 後 |
| 5 | Policy compiler | Agency: 保護 | 必要だが **agency を減らす側**。計測が存在した後でなければコストが分からない。sandbox 既存で仕事は小さい |
| 6 | Credential policy | — | **compile のみに縮退。** `sandbox.credentials` が既に全部やる。`block-secrets.sh` を `denyRead`+mask に置換するのは *agency 増*（誤検知消滅）かつセキュリティ増 |
| — | ~~破壊操作 journal / `rm` shim~~ | — | **削除。** `rm` の PATH shim は `/bin/rm`・`find -delete`・`>`・`python`・Makefile・subprocess に敗れ、**POSIX 意味論を変えるのでスクリプトが壊れる＝直接的な agency 損傷**。さらに **coverage の錯覚**を生む。B2/B3 の事前 checkpoint と `denyWrite` で代替。`rm` に一切触れない `harness trash` のみ残す |

### 10.2 一文の MVP

> すべての agent session は checkpoint された workspace と append-only の event log の上で走り、`harness verify` で自分の正しさを確かめ、`harness state` で自分が何を試して何を棄却したかを compaction を跨いで思い出せる。ゆえに人間は permission prompt を切ってよく、それでも tree は取り戻せる。

### 10.3 リポジトリ構成

```
prohairesis/
  install.sh              # POSIX sh。冪等・--dry-run
  cmd/harness/                     # 単一 Go バイナリ
  internal/
    action/        # 正準 action model（provider 非依存の語彙）
    boundary/      # B0–B6 分類。定義は policy/boundary-classes.yaml から読む
    session/       # lifecycle, lineage(parent_session/task_id)
    checkpoint/    # repo 外 object store + APFS clonefile snapshot  ← 全体の土台
    event/         # JSONL sink, schema 検証
    verify/        # 発見 + 正規化 + failure fingerprint
    state/         # L7: derived(attempt/recurrence/topology) + declared(self-state)
    topology/      # 観測ベースの environment 導出（能動 scan 禁止）
    policy/        # parse, intersect, compile, explain
    metrics/       # friction 計算（transcript + event log）
    trust/         # TOFU pin
    platform/{darwin,linux}/   # clonefile / reflink。OS 依存はここだけ
  adapters/
    claude-code/   # adapter.json(grade C1–C6) + compile.go
                   #   ← repo 内で唯一 Claude Code の存在を知ってよい場所
    generic-exec/  # 非 hook 型 agent 用 sandbox-exec wrapper
  schema/
    harness.{event,session,policy,verify,state,topology,trust,boundary}.v1.json
  policy/
    default.machine.policy.yaml    # 出荷する天井
    boundary-classes.yaml          # B0–B6 を *データ* として
  docs/
    THESIS.md ARCHITECTURE.md BOUNDARIES.md META-STATE.md
    ADAPTERS.md ENFORCEMENT-HONESTY.md   # ← enforcement コードより先に書く
  tests/{golden,scenarios}/
```

install 後のマシン状態:
```
~/.prohairesis/
  config.json                # 版, adapter grade, 天井 hash
  policy/compiled/           # content-hash 付き成果物
  trust.json                 # repo TOFU pin
  sessions/<id>/
    meta.json                # lineage 含む
    events.jsonl             # append-only
    state.json               # L7: derived cache + declared
    snapshot/                # clonefile'd precious files
  metrics/baseline.json
```
**repo 内に作るファイルはゼロ。** checkpoint は `~/.prohairesis/sessions/<id>/store.git`（repo 外の bare object store、`objects/info/alternates` で repo のオブジェクトを読み取り借用）に置きます。

> **当初案からの変更**: プランは `refs/harness/sessions/<id>/<seq>` という repo 内 shadow ref を想定していましたが、
> spike で **`git log --all` が `refs/*` を glob するため可視になる**ことが判明し、「git 表層を1バイトも変えない」
> invariant を初回 checkpoint で破ることが確認されました。プラン §12 で fallback として挙げていた独立 object store に
> 変更しています。詳細と検証結果は [ADR 0001](adr/0001-checkpoint-store-location.md)。

### 10.4 CLI

```
# 可逆性
harness session start|end|list|show <id>
harness undo <id> [--dry-run] [--paths <glob>]   # 実行前に「戻せない外向き effect」を必ず表示
harness diff <id> / harness checkpoint [--label] / harness protect <path>...

# 検証
harness verify [--json]                          # ← Agent が呼ぶ動詞

# meta-cognition (L7)  — すべて任意。呼ばなくても derived は動く
harness state show [--json]                      # goal/attempts/elapsed/recurrence/rejected を1画面
harness state goal <text>
harness hypothesis set <text> [--confidence n]
harness hypothesis reject <id> --reason <text> --evidence <ref>
harness topology [--changed]
harness timeline                                 # attempt × verify × checkpoint を1本の軸で

# policy / 運用
harness policy compile|explain <action>|diff|test
harness trust [--show]
harness report [<id>] / harness metrics [--since 30d] [--baseline]
harness doctor
```

exit code は security-checker から直接踏襲: `0`=clean / `1`=gate failed / `2`=error。graceful degradation も踏襲: ツール不在は `{"skipped":true}` + exit 0。**決して error にせず、決して block しない。**

### 10.5 スキーマ（要点）

- **`harness.event.v1`** — `{ts, seq, session_id, repo_key, type, actor{adapter,agent_session_id,grade}, action{kind,tool,command,argv_sha256,paths,host}, outcome{status,duration_ms}, hook_ms, checkpoint_ref{seq,commit,taken_here}, truncated}`。
  実体は `schema/harness.event.v1.json`。当初案から 2 点変更（[ADR 0002](adr/0002-what-the-event-log-keeps.md)）:
  **(1) `argv` は保持しない** —— プログラム名と全文の SHA-256 のみ。指標が問うのは同一性だけであり、
  本文を持てば event log が秘密の集積地になる。redaction は却下（`block-secrets.sh` と同じゲームを、
  取りこぼしが沈黙する側で再演することになる）。
  **(2) `boundary_class` と `decision` は v1 に存在しない** —— P5 まで分類も enforcement も存在せず、
  常に空のフィールドは「埋めよ」という招待状として働く。schema はバージョン付きなので v2 で足せばよい。
  `kind` は `read|write|delete|exec|network|agent_config|opaque`。**`opaque` は正式な答え**であり、
  写像できない tool を `exec` に丸めることは coverage の錯覚を作る。
- **`harness.verify.v1`** — **security-checker の正規化出力と同一形状**: `{category, skipped, findings:[{severity,message,location}]}` → `{total_score, rank, categories[]}`。`~/security-checker/lib/score.sh` が改修ゼロで verification カテゴリになり、Agent が覚える出力形状は1つで済む。**failure fingerprint もこの正規化形状から作る**（生 stderr からではない）。
- **`harness.state.v1`** — `{derived:{attempts[], elapsed, last_green, recurrence[]}, declared:{goal, current_hypothesis, confidence, rejected[{hypothesis,reason,evidence,at}], known_unknowns[], next_experiment}, lineage:{parent_session, task_id}}`。**`derived` は Agent から書き込み不可（B4）。**
- **`harness.topology.v1`** — `{repos[], branches[], dirs_touched[], processes[], ports[], deps[], endpoints[], changed_resources[]}`。すべて観測由来。読むたび再計算。
- **`harness.policy.v1`** — narrowing 専用語彙。**repo scope に `allow` 動詞が構造的に存在しない。**
- **`harness.boundary.v1`** — B0–B6 を *データ* として。分類が検査可能・テスト可能になる。

### 10.6 `./install` UX

```
$ ./install
  検出: macOS 25.6 (arm64) / APFS / git / Claude Code 2.1.241
        → adapter grade C1–C6 (full, L2 は advisory)

  現在の設定に見つかった問題を先に報告します:
    ! ~/.claude/settings.json の Run(...) 4件はどの tool にもマッチしません。
      あなたの `git push` deny は一度も効いていません。
    ! ~/.claude/hooks/block-secrets.sh は "credentials" を含む全コマンドを
      deny する一方、quoting 次第で `cat $HOME/.env` は通します。
      → sandbox の denyRead + credentials mask で置換します。
    ! sandbox 未設定。2.1.241 は native sandbox を持っています。

  変更予定:
    [user] ~/.prohairesis/ 作成 / hook 登録は install ではなく `prohairesis hooks install`
           （既定は project scope の試用。install.sh は settings.json に触れない。ADR 0002）
    [sudo] /Library/Application Support/ClaudeCode/managed-settings.json  ← 天井

  天井レベル: [1] なし  [2] standard(推奨: B4/B5 deny + sandbox)
              [3] strict(+ allowManagedHooksOnly。signate template の
                  PostToolUse formatter が壊れます。代替を verify 経由で提供)

  friction baseline: 既存 session を検出。install 前に計測しますか? [Y/n]
```
冪等（再実行＝reconcile）／`--dry-run` で全ファイル差分表示／生成物に `_harness` provenance（source hash + version）／`./uninstall` は backup 復元 + 状態ディレクトリ削除。**可逆性を harness 自身にも適用する。**

### 10.7 フェーズと完了条件

| Phase | 内容 | 検証可能なこと |
|---|---|---|
| **P0** ✅**完了** | 既存 transcript から friction baseline 取得（`recurrence_rate`/`resignation_rate` の近似も含む）。**`ENFORCEMENT-HONESTY.md` を enforcement コードより先に書く** | `~/.prohairesis/metrics/baseline.json` に §9 の指標が実データで入る |
| **P1** 可逆性 ✅**完了** | repo 外 object store の checkpoint、`protect` パスの clonefile snapshot、`undo`/`diff`。**policy も hook も deny も無し** | scenario test: `rm -rf src && git clean -xdf && echo garbage > pyproject.toml` → `undo` → tracked/untracked/precious が **byte 一致**。加えて `git log`/`status`/`stash list`/reflog が control repo と **byte 一致**。**この時点で FS 操作の prompt を切ってよい。これだけで本プロジェクトは正当化される** |
| **P2** 観測 + 計測 ✅**完了** | SessionStart/End/PostToolUse shim（全て record-only・`\|\| true`・絶対に block しない）、JSONL sink、`report`/`metrics`、hook 登録コマンド、attach-or-create と自動 checkpoint | 実 session が valid な `events.jsonl` を生む。**時間予算は設けない**（当初の「p50 < 20ms」は導出が存在しなかったため撤回。実測 p50 23ms / checkpoint 込み 58ms を記録するのみ。ADR 0002・`docs/METRICS.md`）。加えて scenario test: state ディレクトリを削除・書込不可・ファイル置換し、ゴミと 200KB payload を渡し、repo 外で走らせても **hook は exit 0**。失われた観測は session ディレクトリと失敗要因を共有しない durable な経路に記録される |
| **P3** verification | 発見（`run_quality_checks.sh`/Makefile/package.json/cargo）→ 正規化、`harness verify`、**failure fingerprint**、有界な context 注入（ポインタ1行のみ） | `~/signate/_template` で `verify --json` が正規化 JSON を返す。注入 context < 50 token。transcript 上で Agent が自発的に `verify` を呼び finding に対処 |
| **P4a** L7 derived | attempt 導出・elapsed・`last_green`・**recurrence 検出と structured signal**・topology（観測のみ）・`state show`/`timeline`・**signal 抑制フラグ（A/B 対照用）** | 3回同一失敗する仕込み repo で `recurrence` signal が count=3・`last_green` 付きで発火する。**Agent 側の協力ゼロで成立すること**（`hypothesis` を一度も呼ばない session でも全部動く）。signal に戦略文言が1つも含まれないことを golden test で固定。**paired-session A/B（signal 抑制あり/なし）で `strategy_revision_rate` の差を測定できること** —— 差が出なければ L7 の signal 部分は効いておらず、再設計対象 |
| **P4b** L7 declared | `hypothesis set/reject`・`goal`・session lineage と carry-over | 新 session 開始時に前 session の `rejected` が carry-over 候補として提示され、**自動 merge されない**。`reject` が `reason` と `evidence` 無しでは記録を拒否する |
| **P5** policy + 天井 + credential | B0–B6 分類表、`policy compile` → settings.json ブロック + managed-settings.json + sandbox。`block-secrets.sh` 退役、`Run()` 修正、`doctor` | golden test。交わり性の証明（広げる repo policy が理由付きで drop）。`Bash(curl:*)` を allow する red-team fixture repo が `trust` まで無効。**かつ `metrics` が P4 比で `interrupts_per_session` を増やしていないこと。増えていたら出荷しない** |
| **P6** adapter #2 | 最小の custom API-loop adapter（C2 が権威的） | 同一 policy が両 adapter で compile され共通シナリオで等価な決定。**core に `claude` の文字列が0件** |

#### 第二の軸のフェーズ（§14）

可逆性の軸（P0–P6）は `Autonomy ≈ Agency × Verification × Persistent State × Recovery` の
ゼロに近い因子順に並んでいる。第二の軸はその因子分解の外にあるため、**P5/P6 との相対順序は未決**とする。
決まっているのは依存だけである。

| Phase | 内容 | 検証可能なこと | 依存 |
|---|---|---|---|
| **A0** 説明コストの測定 | `explanation_cost` を既存 transcript から算出し baseline に加える。**コードより先に測る** | 実データで値が出る。§9.1 の三角測量に組み込まれ、単独では結論に使われない | なし（今すぐ可能） |
| **A1** L7 追補 | `estimate`（declared）と `stale_read` signal（derived） | `estimate_ratio` が verify-green を分母として算出される。仕込み repo で並行変更が `stale_read` を発火させ、**`changed_by` が `unattributed` を超えて主張しない**。signal に戦略文言が無いことを golden test で固定 | **P3**（完了定義）・P4a（attempt 境界） |
| **A2** 説明可能性の記録 | `ACCOUNTABILITY-HONESTY.md` を**先に**執筆 → ADR 0004 → 記録の住所と読者の分離 → gateway | 文書がコードより先に存在する。ADR 0002 の保証が弱められていないか、弱めたなら明記されている。**`interrupts_per_session` が A1 比で増えていないこと（承認要求への転落の検出）。増えていたら出荷しない** | P3・A1 |
| **A3** surrogate 変換パス | 開発工程の不可逆点（レビュー・CI/CD・デプロイ）を可逆な surrogate に変換する設計 | 工程ごとに「harness が代行していないこと」が言えること。§12 反論3 の宿題への回答 | A2 |

---

## 11. Enforcement honesty（`docs/ENFORCEMENT-HONESTY.md`）

**kernel 強制（敵対的 Agent に耐える）**: sandbox 有効時の FS read/write scope、`strictAllowlist` 下の egress、`sandbox.credentials` による mask、天井の完全性（root 所有 + Agent は非 root）。

**advisory のみ（誤りと腐敗への speed bump。意図には無力）**: **L2 のすべて。** PreToolUse hook は Agent 自身の runtime が in-process で呼ぶ。コマンド文字列マッチは `X=rm; $X -rf`・`python` への here-doc・`env -i`・Makefile target・`eval $(printf ...)`・base64・subagent・MCP server で破れる。**L2 を security としてモデル化してはならない。「Agent が普通のミスをしたのを捕まえるもの」かつ「event log を生むもの」としてモデル化する。**

**MVP で全く強制されないもの**: allowlist 済みドメイン経由の exfiltration／`Bash` を迂回する MCP server／sandbox 確立前の effect／他マシンへの effect。

**本物の強制の入手先（強度順）**:

| 機構 | 強度 | コスト | MVP? |
|---|---|---|---|
| CC native sandbox (seatbelt) | FS+network に強い（同一ユーザー内） | 低。一部ビルドが壊れる | **Yes, P5** |
| harness 生成 `sandbox-exec` profile（他 agent 用） | 同等。profile 保守は実作業 | 中 | P6 |
| Agent 専用 unix user | 強い（Keychain・`~/.ssh`・天井に触れない） | 高。所有権・GUI 認証・開発ツールが壊れる | No。escalation path として文書化 |
| Container / VM | 最強 | 最高。「自分のマシンのどの repo でも動く」前提を壊す | No（§12 の最強反論） |

**MVP の正直な保証、一文**: *敵対的ではなく混乱した Agent に対して、workspace 内のすべての effect は復元可能で、すべての effect は記録され、試行と棄却の履歴は session と compaction を跨いで保持される。workspace 外の effect は P5 以降 kernel sandbox で bound される。積極的に脱出しようとする Agent に対しては何も守らない。*

これは permission prompt を切ることを正当化するのに必要十分 —— 現代のコーディング agent の失敗モードは圧倒的に malice ではなく error だから。本番 credential を持って untrusted repo で untrusted agent を走らせるのに必要な保証では **ない**。harness はそれを声に出して言う。

---

## 12. 失敗モードと最強の反論

### 失敗モード
1. **data 重量 repo での snapshot コスト** — `~/signate/_template` の `data/raw` は数十 GB。→ **copy ではなく `denyWrite` で守る。** `protect` の既定は *deny*、*snapshot* は小さいパスへの明示 opt-in。上限超過で大声の警告。
2. **undo の非対称性** — push 済みブランチ・送信済みメール・削除済みクラウド資源・DB の行は戻らない。→ **`undo` は動作前に、event log の B5 から「戻せない外向き effect」を必ず列挙する。**
3. **sandbox がビルドを壊し、ユーザーが sandbox を切る** — 最悪の結末。守られていると信じたまま L0 無しに戻る。→ `doctor` は sandbox 無効を **error** 扱い。`excludedCommands` で「切る」ではなく「狭める」逃げ道。
4. **policy の腐敗** — 既に起きている（`Run(git push)` が何ヶ月も無効）。→ **手書きせず compile。** `doctor` が adapter の実 tool 名前空間に照合し、死んだ規則で fail する。
5. **context tax の creep** — 各 Phase が「ほんの少しだけ」注入したがる。→ 2% ハード上限を **注入コードパス自身が強制**（超えるくらいなら truncate してログ）。コンポーネント別計上で犯人を特定。**L7 を pull 型にしたのは主にこの理由。**
6. **評価器のレイテンシ** — 毎 tool call の評価は累積的で実在する agency 税。→ 計測対象にする。**Go を選んだのはこの不確実性を先に消すため。**
7. **trust の bootstrap** — `harness trust` は5回目の prompt で反射的に押される。→ repo あたり生涯1回に保ち、**untrusted の既定を「不便」ではなく「使える」水準にする**。
8. **L7 特有: Agent が meta-state を読まない。** 最大の実効リスク。強制はしない（強制は Agent の制御であり本層の存在意義に反する）。→ 緩和は3つ: (i) derived は協力ゼロで動く、(ii) recurrence signal は push される、(iii) `verify` 出力に compact な meta ヘッダを載せる。効果は `recurrence_rate` で測り、下がらなければ **層ごと再設計する**。
9. **L7 特有: failure fingerprint の誤同定。** flaky test・非決定的メッセージ・タイムスタンプ。→ 生 stderr ではなく正規化 finding から hash。count は confidence 付きで提示し、**決して自動で行動しない**。
10. **L7 特有: topology が CMDB になる。** → 「観測であってモデルではない」規律、能動 scan 禁止、キャッシュを真実にしない、を schema と code review で固定。
11. **L7 特有: signal が助言に堕ちる。** `"you should try X"` を1行足したくなる圧力は常にある。→ **signal のフィールド集合を schema で閉じる**（`available_recovery` は既存 checkpoint の列挙のみ）。golden test で戦略文言の不在を固定。
12. **L7 特有・最大の長期リスク: externalization が augmentation に膨らむ。** 「ここで要約すれば便利」「過去 session を検索できれば」「有望な仮説を上に出せば」—— どれも局所的には正しく見え、累積すると *harness が Agent の代わりに考える AI システム* になる。§6.0 の非目標表を破ることになる。→ 機構的防御を2つ: (i) **`internal/state/` と `internal/topology/` からモデル呼び出し・ネットワーク I/O・埋め込みライブラリへの依存を禁止する architecture test**（CI で import グラフを検査し、違反でビルド失敗）、(ii) §6.0 のスコープテスト（「T に知っていて T+n に失うもの」）を PR テンプレートの必須項目にする。**この2つが緩んだ時点で L7 は別プロダクトになっている。**
13. **harness が自らの敵になる** — 最も可能性の高い長期失敗。事故のたびに規則が増え、1年後には綺麗な README を持った制限フレームワークになる。→ friction 予算と P5 のゲート（「interrupts が増えたら出荷しない」）**だけ**が本当の防御であり、機械的に強制されねばならない。

### 最強の反論
1. **「container/VM を使え。厳密に強く、1日で終わる」** —— 最強の反論。enforcement については正しく thesis については誤り: container は *capability 削減* であり、その摩擦（Keychain 無し・ローカル toolchain 無し・GPU 無し・同期・「どの repo でも動く」の喪失）はまさに原則3が禁じる agency 損傷。**賭けは「error 失敗モデルに対しては同一マシンでの可逆性が別マシンでの隔離に勝つ」。** 擁護可能だが賭けであり、賭けとして明示すべき。**譲歩: 真に untrusted な repo や真に敵対的な agent には container が勝つ。harness は「container を使え」と言うべきで、ふりをすべきではない。** なお L7 は container でも必要であり、この反論の影響を受けない。
2. **「ベンダが呑み込む」** —— 既に部分的に的中（sandbox・credential mask・checkpoint が 2.1.241 に既存と判明し当初 MVP の半分が消えた）。**残る堅牢な中核は verification contract・friction metrics・Meta-State** —— いずれも agent runtime ではなく *repo* と *人間* と *task の履歴* についてのものであり、だからこそ agent ベンダは作らない。**1つに削るならこの3つに削る。policy engine ではない。**
3. **「マシンを離れた瞬間に可逆性は虚構」** —— 真であり譲歩。応答は B5 クラスと **surrogate 戦略**: 本当の設計作業は undo を作ることではなく *不可逆 action を可逆なものに変換すること*（main→branch、send→draft、live key→test key、apply→plan）。**後日専用の設計パスに値する。**
4. **「Agent が回避できる policy 層は security theater。theater は偽の安心を生むぶん無より悪い」** —— 半分譲歩。緩和は構造的: 全 event の `enforcement` フィールド、`doctor` の adapter grade 表示、enforcement コードより **先に** 書く `ENFORCEMENT-HONESTY.md`。
5. **「L7 は Agent が自分で書けるスクラッチファイルにすぎない」** —— **declared 半分についてはその通り**（ただし schema 付き・verification 結果に結合されている点が違う）。**derived 半分については成立しない**: 前提がまさに「Agent は状態を失う」ことであり、失う主体に自己記録を任せるのは循環。加えて derived は compaction と session 境界を跨いで生き残り、checkpoint と verification 結果に join されている —— この join は harness にしか見えない。
6. **「1つの agent のための抽象化を作っている」** —— 実在するリスク。adapter #2 は古典的な「永遠に来ない第二実装」。緩和は P6 を **継ぎ目を実際に反証できるほど早く** 作ること。無期限に滑るなら、正しい対応は抽象化を **畳んで** 「これは Claude Code harness だ」と認めること。

### 現時点で不確実なこと（明示する）
- ~~**shadow ref が実運用で十分に不可視か。**~~ → **解消（否）。** spike で `git log --all` に露出することを確認し、
  独立 object store に変更した（[ADR 0001](adr/0001-checkpoint-store-location.md)）。新たな未確認事項として、
  alternates で借用したオブジェクトが history rewrite + gc で失われうる点があり、`Verify` が復元前に検出して
  **部分復元せず拒否する**実装にしてある。
- **`allowManagedHooksOnly` が正味プラスか。** 最強の攻撃 vector を閉じるが、ユーザーが実際に使っているパターンを壊す。opt-in + コスト明示に傾くが確信を持つ根拠がない。
- **H1 そのものが未検証。** `Autonomy ≈ Agency × Verification × Persistent State × Recovery` は作業仮説であり、P4 完了時の `mean_uninterrupted_run` が唯一の判定材料。反証されたら縮退する（§9.2）。
- **friction メトリクスが実世界のノイズの中で回帰を検出できる感度を持つか。** session 間分散が信号を埋める可能性。L7 については paired-session A/B（§9.1）で対処するが、`interrupts_per_session` のような session 全体の指標には同じ手が使えず、時系列 baseline に頼らざるを得ない。
- **attempt 境界の導出が実際に妥当か。** `verify` 呼び出しと checkpoint を境界とする heuristic が、実 session の作業リズムと合うか未検証。合わなければ recurrence 検出の分母が壊れる。P4a の最初の spike で確かめる。
- **説明義務の記録が「住所と読者」の分離だけで成立するか（§14.3）。** ADR 0002 の *durable ∧ unencrypted ∧ agent-readable* は連言なので、宛先を分ければ崩せるはずだが、DRY を破らずに第二の住所を立てられるかは未確認。破れないなら、透明性の要求を縮退させて明記する側が正しい。ADR 0004 で決める。
- **gateway が経路を強制できるか（§14.3）。** 強制できれば §11 が「MVP で全く強制されない」に挙げた exfiltration と MCP server 迂回を初めて閉じられ、できなければ L2 と同格の advisory にすぎない。**この差は機能の説明文をまるごと変える。** 採用候補の性質が確認できるまで、enforcement 表に行を書かない。
- **工数見積もりの分母が定義できるか（§14.4）。** 人間の「2〜3日」と session の wall clock は測っているものが違う。verify-green を完了定義に据えても、見積もりが指していたスコープと実際の到達点が一致する保証はない。**定義できないなら、ADR 0002 の p50 と同じく、数字を調整せず落とす。**
- **`stale_read` の read 集合が実用上十分に完全か（§14.4）。** `truncated` により保証はない。見逃し率が実 session で高ければ、この signal は「無いよりまし」ではなく §12 反論4 の theater 側に落ちる。A1 の spike で見逃し率を測ってから出荷を判断する。
- **`explanation_cost` が質問の抑制で下がってしまわないか（§14.5）。** 訊かずに間違える Agent はこの指標を改善する。単独で読まず、`resignation_rate` と `recurrence_rate` と併せた三角測量でのみ結論する。

---

## 13. 検証方法（end-to-end）

1. `./install --dry-run` が全変更を表示 → `./install` → `doctor` が緑、かつ現行設定の3つの問題（inert な `Run()`・`block-secrets.sh` の誤検知・sandbox 未設定）を報告
2. `tests/scenarios/destroy_and_restore`: 一時 repo で `rm -rf src && git clean -xdf && echo garbage > pyproject.toml` → `undo` → byte 一致。**かつ `git log`/`status`/`stash list`/reflog が harness 無しの control repo と byte 一致**
3. `tests/scenarios/recurrence`: 同じテストが3回失敗する仕込み repo で、`recurrence` signal が count=3・`last_green` 付きで発火し、**Agent が `hypothesis` を一度も呼ばなくても** attempt history と topology が完全であること
4. `tests/golden/signal_has_no_strategy`: signal の全フィールドが schema の閉じた集合内で、戦略文言を含まないこと
5. `tests/arch/l7_has_no_model`: `internal/state/` と `internal/topology/` の import グラフに、モデル呼び出し・ネットワーク I/O・埋め込みライブラリが1つも含まれないこと（§6.0 の非目標を機構で固定）
6. `harness policy test`: golden な action→決定表。旧 `block-secrets.sh` の誤検知ケース（`cat docs/credentials-design.md` 等）を regression corpus として同梱し **false positive 0**
7. red-team fixture repo（`.claude/settings.json` で `Bash(curl:*)` を allow）が `harness trust` まで無効であること
8. 実 session を `~/signate/tech-ocean-student-cup-2026` で流し、`metrics` が baseline 比で `interrupts_per_session` 非増加・`context_tax` < 2%・`recurrence_rate` 低下を示すこと
9. `verify --json` が `~/signate/_template` で `run_quality_checks.sh` を発見し正規化形状で返すこと
10. `./uninstall` 後にマシンが install 前と byte 一致であること

---

## 14. 第二の軸 —— 説明可能性、および開発プロセス全体への拡張

§1–§13 は**ひとつの軸**の上に組まれている: *effect が取り消せるなら、事前に許可を求める必要はない*。
可逆性 → agency、という軸である。

この節が足すのは**第二の軸**である。契機は次の観察:

> 実装そのものは AI コーディング agent で高速化した。だが要件整理・テスト・レビュー・デバッグ・
> 修正・CI/CD は開発者に残っている。AI に一つずつ指示を出すだけでは、開発者が AI の作業員になる。
> 問題は「AI がコードを書けるか」ではなく、**AI を含む開発プロセス全体をどう設計するか**にある。

これは §4 の「Agent の制御 ではなく 環境の制御」と同じ方向を向いているが、対象が違う。§4 が
設計するのは *effect の基盤*（何が取り消せるか、何が観測されるか）であり、この節が設計するのは
**プロセスの説明 (account)** —— なぜそうなっているのか、誰が責任を負うのか、その信念はまだ有効か、
実際どれだけ掛かったのか、人間がそれを説明するために何を払っているのか。

**二つの軸は直交する。** 取り消せる操作にも説明義務は残り、取り消せない操作の説明義務は特に重い。
可逆性は説明可能性を含意しないし、その逆も成り立たない。

### 14.0 責任の座は二つある

προαίρεσις の教義は「責任は自分に属するもの (*τὰ ἐφ' ἡμῖν*) に従う」である。README はこの harness を
「Agent の prohairesis を守るために存在する」と述べている。第二の軸はその教義を**人間の側**に適用する:

> **Agent の agency がいくら増しても、それを実行しようと試みた人間に責任がある。**

責任が名目でなく実質であるためには、人間が「何を起動したことになるのか」を見られなければならない。
ゆえに **§14.1（可視性）→ §14.2（説明義務）は前提と帰結の関係**にあり、独立した二機能ではない。

この立て方は記録の粒度も決める。答えるべき問いは「Agent は何をしたか」ではなく
「**人間は何を起動したことになるのか**」であり、単位は tool call ではなく *人間のひとつの意図に
紐づく effect の束* である。

### 14.1 「透明性」を三つに割る

§1 が Capability / Agency / Autonomy / Harness を「混同してはならない4語」として分けたのと同じ理由で、
透明性も分ける。混ざったまま実装すると、**手に入らないものを手に入れたことにしてしまう**。

| | 何が透明になるか | この層で手に入るか |
|---|---|---|
| **(a) 挙動の透明性** | どのファイル・どのコマンド・どの外向き通信 | **既にある。** L5 が答える。粒度は ADR 0002 が確定済み（*同一性*であって*内容*ではない） |
| **(b) 理由の透明性** | なぜそれを選んだか | **一部設計済み。** declared state の `rejected` は `reason` と `evidence` を必須にしている（§6.4）。§14.3 が外向きに拡張する |
| **(c) 機序の透明性** | なぜそのトークンが出たか | **構造上、手に入らない。** harness はモデルの外にいる |

**(c) を持っていると主張した時点で、本プロジェクトは §11 の最悪の失敗モード（「静かな degrade こそが
harness を『安心の錯覚』に変える」）に入る。** そして §6.1 は既にこれを扱う語彙を持っている ——
declared state は「**Agent の主張として扱う。真偽を判定しない**」。Agent の自己説明や思考過程の開示は、
機序のトレースではなく **declaration** である。証拠ではなく主張。この区別を保つことが (c) に対する
唯一の誠実な態度であり、混ぜないことを機構で守る。

**透明性 theater は §12 反論4 と同型である。** 「回避できる policy 層は security theater であり、
theater は偽の安心を生むぶん無より悪い」。完全に見えるが実は欠けているログ、機序トレースであるかの
ように提示された自己申告は、支えられない信頼を発行する点で同じ害を持つ。

> **規律**: 透明性を主張する文書は、**何が透明でないかを同じ強さで書く**。
> `docs/ENFORCEMENT-HONESTY.md` と同じ方法で、gateway のコードより**先に**書く（P0 と同じ手順）。

### 14.2 透明性の主たる担い手は derived でなければならない

「LLM がやっていることを明確化する」の最も自然な実装は、**Agent に毎回説明させること**である。
これは §6.1 の不可侵規則に真正面から抵触する:

> 「meta-state を書け」という義務を Agent に課さない —— 課した瞬間それは Agent の制御であり
> micromanagement。

ここから非自明だが強い帰結が出る:

> **説明可能性の本体は「effect の記録から機械的に再構成できること」であって、
> 「Agent が正直に語ること」ではない。** declared はあくまで純粋に加算的な補助である。

自己申告に依存する透明性は、申告が偽であるケースでちょうど無力になる。**倫理的にも derived が強い。**

機序に触れずに「白箱」と呼べるものの実体は、次の連鎖である:

```
人間が何を承認したか            (§14.3 gateway)          未着手
  → どの effect が起きたか       (L5 event ledger)         ✅ P2
  → その前後のツリー状態         (checkpoint_ref: 全 event) ✅ P1/P2
  → その時 Agent が何を宣言したか (L7 declared)             P4b
  → 検証は何と言ったか           (L6 正規化 verify 結果)     P3
```

`checkpoint_ref` は「*Written on every event, not only on the ones that took a checkpoint*」
（`schema/harness.event.v1.json`）なので、**任意の時点のツリーは既に再構成可能**である。穴は下二段。
すなわち **この節の大半は L6 の存在に依存する。P3 を飛ばすとこの連鎖に「本当に正しかったのか」の
段が空いたままになる。**

### 14.3 説明義務 —— gateway、および ADR 0002 との衝突

外向き effect について §6（THESIS）は「trust-free な手は3つしかない: capability を与えない /
可逆な surrogate に変換する / 人間に gate する」と述べる。**説明可能性はこの3択のどれでもない。**
安全性の選択肢ではなく、安全性とは独立に残る義務である。ここは現行設計の空白である。

**gateway の位置づけは、経路を強制できるかどうかで完全に変わる。** §11 の enforcement 表の規律で言えば:

| 条件 | 強度 |
|---|---|
| Agent の egress が gateway を通る以外に道がない（L0 sandbox が gateway 以外への外向き接続を落とす） | **本物の強制。** §11 が「MVP で全く強制されない」に挙げた *allowlist 済みドメイン経由の exfiltration* と *`Bash` を迂回する MCP server* を初めて閉じられる。gateway は**ドメインではなく payload を見る**唯一の点だから |
| 通ってくれれば見える、だけ | **advisory。** L2 と同格。強そうな見た目をした speed bump であり、§11 の「静かな degrade」に該当する |

**未確定**: 採用候補（openclaw 等）が前者の性質を持てるか。ここが決まるまで、この機能が
enforcement 表のどの行に入るかは決められない。**決まる前に「透明」と呼んではならない。**

#### ADR 0002 との衝突 —— 倫理 対 倫理

説明義務は ADR 0002 が「受け入れたコスト」として明記したものを、受け入れられなくする:

> **Bad, and accepted.** A digest cannot be read back. Debugging "which command was that" from
> the log alone is **impossible by construction**.

event log は `argv[0]` と SHA-256 ダイジェストしか持たない。第三者に「なぜこの外向き effect を
起こしたのか」を説明するには、同一性では足りず内容が要る。

**しかし ADR 0002 を覆すのは誤りである。** その動機もまた倫理的だからである ——
「急いで一度打った credential までが、*durable* で、*暗号化されておらず*、*Agent が読める*
ファイルに蓄積する」。これは技術的都合ではない。**透明性の要求と秘匿の要求が、どちらも倫理的
根拠を持って正面衝突している。** 片方を技術的トレードオフとして処理してはならない。

ただし ADR 0002 の制約は *durable ∧ unencrypted ∧ agent-readable* の**連言**である。ゆえに
解決は「どれだけ記録するか」ではなく **「どこに置き、誰が読めるか」** にある:

| 宛先 | 要件 |
|---|---|
| Agent 自身 | secrets を含んではならない（Agent が読む） |
| 人間のオペレータ | 濃くてよいが、**Agent が読めない場所**に置く |
| 第三者（説明義務の相手） | 生の内容ではなく**再構成可能性**があればよい |

DRY（一つの事実に一つの住所）を破らずにこれを成立させることが設計の仕事であり、**ADR 一本を要する
（ADR 0004、未執筆）。** ADR 0002 を amend するか、別の住所を立てるかはそこで決める。

#### 内向きの顔 —— 「なぜこのコードなのか」

同じ substrate の内向きの面が、**コードそのものより失われやすい背景情報**である。本リポジトリは
既に `docs/adr/` を持ち、CONTRIBUTING は「設計判断は ADR へ」「すべての ADR はどの原則に答えているかを
書く」と定めている。**rationale の住所は既に決まっている。** DRY から、正しい手は新しい rationale
ストアを作ることではなく、**ADR / commit / PR を event ledger (L5) と正規化 verify 結果 (L6) に
join できるようにすること**である。§12 反論5 の「この join は harness にしか見えない」と同じ論理構造。

**境界**: §6.0 のスコープテストにより、session 内で失われる「なぜ」は declared state の `rejected`
（P4b）が扱う。**数ヶ月前の他人の判断は externalization ではなく augmentation** であり、L7 の非目標表の
「insight / lesson learned の抽出」に該当する。同じ層に入れれば §6.0 が壊れる。join は**参照**であって
**解釈**ではない。

### 14.4 L7 への追補 —— 見積もり較正と、信念の陳腐化

#### (④) 見積もり較正

LLM の工数見積もりは、**自分が存在しなかった時代 —— 人間が実装していた時代の工数を参照している
可能性がある。** 「2〜3日かかります」という申告は当てにならず、実際に LLM を用いた開発では
1日未満で終わることが多い。

**なぜそうなるかの候補は §15（H2）にある**: Agent は「2〜3日」を概念として言えるが三日を経過したことが
無く、較正の基準になる持続を持たないので、訓練データにある数値を借りる。以下の ⑥ と合わせて、
**H2 が正しければ両者は一つの欠落の二つの症状である。**

これは §6.1 の derived / declared 分割にそのまま乗る。`estimate` は declared であり、構造的には
§6.4 の `confidence`（「数値。harness は解釈も gate もしない」）と同型である。実測側は既にある ——
`session.Started`、各 event の `ts`、`outcome.duration_ms`。**欠けているのは宣言側だけ**であり、
新機構はほぼ要らない。

§6.0 のスコープテストに素直に通る: Agent は T に「2〜3日」と言い、T+n には**そう言ったこと自体を
忘れ、40分で終わった事実を一度も知らない**。

**単なる較正の問題ではない。** 過大な時間見積もりはそれ自体が pricing mechanism である —— §3(1) の
「permission prompt は不可逆性の価格付け機構」と同じ形をしている。「3日かかる」と信じている Agent は
自らスコープを狭め、部分実装を提案し、早く諦め、人間に判断を仰ぐ回数が増える。すなわちこれは
**既存の friction 指標（`resignation_rate` / `interrupts_per_session` / `mean_uninterrupted_run`）の、
新しく特定された原因**である。新プロダクトではなく、既存指標族への新しい入力。

**線**: harness が出してよいのは事実の並置までであり、**補正した数字を出した瞬間**に §6.0 の非目標
「仮説のスコアリング・有望度判定 ＝ 判断の代行」に落ちる。「だからもっと短く見積もれ」は1行も含めない。

**測る前に潰すべき交絡**（ADR 0002 が「導出のない閾値は設けない」を確立している以上、ここも同じ規律に従う）:

1. **単位が違う。** 人間の「2〜3日」にはミーティング・レビュー待ち・コンテキストスイッチ・睡眠が
   入っており、session の wall clock はほぼ打鍵時間である。比を取っても意味を成さない。
   `internal/metrics/metrics.go` の `mean_uninterrupted_run` が既に同種の但し書きを実践している。
2. **スコープがずれる。** 見積もりは機能全体、実際に出来たのは部分、が最大の交絡。**本プロジェクトが
   持つ唯一の交渉不可能な完了定義は `verify` が green になること**であり、見積もりを verify-green
   条件に紐づけて初めて測定可能になる。**P3 依存。**
3. **原因の特定。** 「人間時代の工数を参照している」は有力だが唯一ではない（タスク記述の曖昧さに
   対する安全側の見積もり、不確実性を時間単位で表現する学習された慣習）。測定は仮説を前提とせず、
   **区別できる形**で設計する。

#### (⑥) 並行変更による信念の陳腐化

人間や他の agent が同じディレクトリで並行して作業していると、LLM の挙動が不審になる。
**一度読み込んだファイルが変更されていることがある、という認識がない。** かといって毎回
全ファイルを探索するべきではない。

**§6.0 のスコープテストは、この現象の語彙を持っていない。** テストが問うのは *loss*（T に知っていて
T+n に失う）であり、これは *invalidation*（持ち続けている信念が偽になった）である。Agent は失って
いない。実際 §6.3 topology は「この session が触った」対象に閉じており、**ワークスペースを乱すのが
自分だけである世界**を前提にしている。並行アクターは `internal/session/lock.go` の repoLock
（「二つの hook が競合して session を作る場合をまさに防いでいる」）で **session 層では想定済み**だが、
**ファイルに対する信念の層ではモデル化されていない。**

この拡張は §6.0 の意図的な閉包を広げるため、**[ADR 0003](adr/0003-invalidation-is-in-scope.md) で
決定を記録する。**

**全探索は最初から要らない。** 検査対象はリポジトリ全体ではなく、**この session が実際に読んだ
ファイル集合**である（`action.paths` として ledger にあり、小さい）。各 read イベントには
`checkpoint_ref` が付いているので、**読んだ時点の内容は既に復元可能**であり、`diff since read` は
新機構ではなく P1+P2 の成果物に対する join である。`stat`（mtime+size）を先に取り、不一致のものだけ
hash すれば、コストは読んだファイル数に比例する程度に収まる。**ただし ADR 0002 の規律に従い、
実測前に閾値を発明しない。**

**帰属は「自分ではない誰か」までしか言えない。** 言えるのは `変更された ∧ ¬(自分の write 集合)` だけ
である。event schema が既に規律を示している —— `kind: "opaque"` について
「*"opaque" is a first-class answer, not a failure. Collapsing an unmappable tool into a real kind
manufactures the illusion of complete coverage*」。同じく **`changed_by: unattributed`** が正しい答えで
あり、「人間が編集した」と書いてはならない。

**発火点**: §6.2 が持つ push 機構は現在 `failure_recurrence` ひとつだけである。これはその**二つ目**の
structured signal として入る。タイミングは §6.2 が既に導出している **attempt 境界**（`verify` 呼び出しと
checkpoint）が自然であり、tool call ごとの検査は context tax と latency の両方に効くため採らない。

```json
{"signal":"stale_read","paths":["internal/session/session.go"],
 "read_at_seq":41,"changed_by":"unattributed",
 "available_recovery":["diff_since_read"]}
```

規律は `failure_recurrence` と同一である。**「読み直せ」と書いてはならない** —— それは戦略であり
§6.0 の非目標。`available_recovery` は §6.2 が明示的に許した「既存の affordance の列挙」なので、
そこまでが線である。

**これは agency を増やす側である。** 現状この問題は人間が処理している ——「さっきそのファイルを直したから
読み直して」という割り込みであり、`interrupts_per_session` に計上される人間の決定点である。外部化すれば
その割り込みが消える。§4 の corollary（「記憶・時間・状態・topology は環境が提供する affordance である」）に
そのまま乗り、しかも**見出し指標を下げる方向に効く**、§5 の friction 予算に対して正面から正当化できる
数少ない機能である。

**既知の穴（正直に）**: schema の `truncated`（「*The event exceeded the size at which it can still be
written with a single write(2), so paths were dropped. Set rather than silently shortening*」）により、
**read 集合は完全性を保証されない**。この機能は「変更を見逃さない」とは言えず、「見逃したことは
`truncated` から分かる」までである。

**危険（三つ）**:

1. **ブロックに化ける。** 陳腐化したファイルへの write を止めたくなる。L5 の「絶対に何もブロックしては
   いけない」と default-non-blocking の破棄であり、lock も同断。**署名するのは記録であって、拒否ではない。**
2. **daemon / file watcher に化ける。** §6.3 の「マシン全体を能動 scan しない」に抵触する。要求時に
   ledger + stat から計算するだけに留める。
3. **キャッシュが真実になる。** §6.3 の「キャッシュを真実として持たない。読むたびに再計算する。腐る
   第二の情報源を作らない」。**腐った情報を検出する機能が腐る情報源を作る**のが、この機能の最大の失敗モード。

### 14.5 人間側の説明コスト —— 測られていない friction

§9 の指標族は `context_tax`（harness → Agent 方向）しか持たない。だが実際には逆方向の負担が増えている:

> AI が賢くなるほど、**AI にコンテキストを説明する仕事**が開発者の新しい負担になりつつある。

**これは測定対象として存在しない。** そして本プロジェクトの流儀では、**測れないものは設計しては
ならない**。ゆえにここが第二の軸の最初の一手になる。§14.4(⑥) の効果もこの指標で測れる ——
「そのファイル直したよ」はまさに人間が払っている説明コストだから。

指標は §9 に追加する（`explanation_cost`）。**注意**: この指標は Agent の質問を抑制する方向に
最適化してはならない。質問を減らすことは容易であり（訊かずに間違える）、それは
`interrupts_per_session` を下げて `resignation_rate` を上げる。§9.1 の三角測量の規律をここにも適用する。

### 14.6 残りの工程 —— 代行ではなく surrogate 変換

要件整理・レビュー・CI/CD について、現行設計は何も持っていない。素直に作ると §12 失敗モード13
（「harness が自らの敵になる」）と §6.0 の非目標（「Agent の代わりに考える AI システム」）に直撃する。

**逃げ道は既に設計文書の中にある。** §12 反論3 の surrogate 戦略 ——「本当の設計作業は undo を作ることでは
なく *不可逆 action を可逆なものに変換すること*（main→branch、send→draft、live key→test key、
apply→plan）」であり、そこで「後日専用の設計パスに値する」と明示的に先送りされていた宿題である。

> **定式化**: 工程を harness が**代行**するのではなく、**工程の不可逆点を可逆な surrogate に変換する。**

レビュー・CI/CD・デプロイはこの適用先そのものである。この定式化を守る限り、原則を破らずに入る。
逆に「harness が要件を整理する」「harness がレビューする」と言い始めた時点で、それは別プロダクトである。

### 14.7 この節が守るべき規律（まとめ）

1. **透明性は三つに割り、(c) は構造上到達不能と宣言する。** 主張する文書は、何が透明でないかを同じ
   強さで書き、**コードより先に書く**。
2. **説明可能性の本体は derived である。** declared は加算的な補助にとどめ、Agent に説明を義務づけない。
3. **説明義務は事後的（記録と再構成可能性）でなければならない。** 事前的（承認）に堕ちた瞬間、それは
   §3(1) が「harness に対するバグ報告」と呼ぶ permission prompt であり、P5 の出荷ゲート
   （`interrupts_per_session` が増えたら出荷しない）が掛かる。
4. **harness は記録を持つ。説明する主体は人間または Agent であって harness ではない。**
   「なぜこうしたかを要約」は §6.0 の非目標「insight / lesson learned の抽出」そのものである。
5. **持っていない帰属を捏造しない。** `unattributed` は一級の答えである。
6. **この節のどの機能も、§5 の friction 予算に対して agency 増または中立として正当化できること。**

### 14.8 依存関係と未確定事項

**依存**: ①（§14.5）以外の全項目が L6（正規化 verify 結果）に依存する。②⑤⑦ は連鎖の最終段として、
④ は完了定義（verify green）として、⑥ は attempt 境界として。**P3 を飛ばす選択肢はない。**

**執筆が先行すべき文書**（P0 と同じ手順、コードより先）:

- `docs/ACCOUNTABILITY-HONESTY.md`（未執筆）—— §14.1 の (a)(b)(c) と、gateway が enforcement か
  advisory かを述べる。gateway のコードより先。
- [ADR 0003](adr/0003-invalidation-is-in-scope.md) —— §6.0 のスコープテスト拡張（執筆済み）。
- ADR 0004（未執筆）—— 説明義務の記録と ADR 0002 の関係。「住所と読者」で解けるか。
- ADR 0005（未執筆・**H2 の検証待ち**）—— §6.0 の境界を「形式/質料」で言い直すか（§15.2）。
  併せて `docs/THESIS.md` §3(2) の診断を書き換えるか（§15.3）。**未検証の仮説で確定文を書き換えない。**


---

## 15. H2 —— 欠けているのは時空の概念ではなく、直観の形式である

§14 が列挙した失敗（見積もりが当たらない、読んだファイルの陳腐化に気づかない、環境を毎回再発見する）は、
別々の欠陥ではなく**一つの欠落の別々の症状**である可能性がある。この節はその診断を、H1 と同じ形式の
**作業仮説 H2** として立てる。

### 15.0 区別

カントにおいて空間と時間は、世界の中の事物でも経験から得られた概念でもなく、**感性のア・プリオリな
直観形式**（時間は内感の形式、空間は外感の形式）である。それは*概念*ではなく*直観*であり、単一かつ
直接的で、**あらゆる所与がそこにおいて与えられるところの形式**である。対象として思い浮かべるもの
ではなく、何かが与えられる際の与えられ方そのものを指す。

ニュートンにおける絶対時間・絶対空間は、これとは範疇が違う。**表象可能な対象**であり、座標であり、
「それ自体の本性から、外的な何ものとも関係なく一様に流れる」容器である。**それについて語ることができる。**

### 本プロジェクトの作業仮説（H2）— *証明済みの命題ではない*

```
H2:  LLM は時空を「ニュートン的な表象される対象」としては十全に扱えるが、
     「カント的な直観形式」としては持たない。
```

- **主張の内容**: Agent は timestamp・duration・順序・パス・隣接を**概念として完全に操作できる**。
  欠けているのは、自らの認識がそこにおいて与えられる形式としての時間と空間である。すなわち
  **自分の信念が「いつのものか」という指標を、自動的には帯びない。** 時間について語れることと、
  時間の中で経験を持つことは別である。
- **何が H2 を反証するか**（両側）:
  1. **形式を外部から供給しても改善せず、prompt での指示（「時間経過に注意せよ」）と差が出ない場合。**
     それは欠けていたのが形式ではなく単なる注意喚起だったことを意味し、§4 の「環境の制御 > Agent の制御」が
     この領域では成り立たない。→ L7 は prompt レベルの緩和に縮退すべきであり、その方がはるかに安い。
  2. **逆に、ニュートン的な素材（生の timestamp・elapsed・read 位置）を context に置いても行動が変わらない場合。**
     それは「概念は持っている」という H2 の前提の後半が偽であることを意味する。→ 外部化では埋まらないので、
     L7 は人間向けの記録に縮退すべきである。
- **H2 に依存しないもの**: 可逆性（P1）と観測（P2）。**賭けているのは L7 の診断であって、プロジェクトの存在ではない。**
- **H2 はカントが人間認識について正しいことに依存しない。** ここで使っているのは形而上学的主張ではなく
  **形式と質料を分ける道具**であり、超越論的観念論が偽であってもこの区別は仕事をする。

### 15.1 これが説明してしまうもの

H2 の価値は新しい機能を要求することではなく、**既に観測された症状を一つの原因に束ねる**ことにある。

| 症状 | H2 による説明 |
|---|---|
| **見積もりが人間時代の工数に張り付く（§14.4 ④）** | Agent は「2〜3日」を*概念として*言えるが、三日を**経過したことがない**。較正の基準になる持続の経験が無いので、訓練データにある数値 —— すなわち人間が実装していた時代の数値 —— を借りる。**④は H2 の帰結であって独立の現象ではない** |
| **読んだファイルの陳腐化に気づかない（§14.4 ⑥）** | 時間が内感の形式であるなら、信念は自動的に「*いつ*のものか」の指標を帯びる。形式が無ければ read の結果は*内容*であって*ある時点の内容*ではない。**Agent には「あれ以来」が無い。** timestamp は ledger にあるのに、信念の側がそれに索引付けされない |
| **環境を毎回ゼロから再発見する（§6.3 `rediscovery_cost`）** | 空間についての同じこと。パスを座標として表象できても、workspace が**一つの直観された空間**として与えられていないので、その部分領域という形で保持されない |
| **compaction が効く（§6.5）** | カントの言う「構想力における再生」—— 先行するものを保持しつつ後続を捉える総合 —— が外部化されていないものはすべて失われる。**ledger はこの総合の外部義肢である** |

### 15.2 §6.0 への含意 —— 形式は供給する、質料は供給しない

§6.0 は L7 を *externalization のみ*と規定し、ADR 0003 がそれを *loss ∪ invalidation* に広げた。
H2 はこの線を**より鋭く言い直せる可能性**を与える:

> **L7 は形式を供給し、質料を供給しない。**
>
> - **質料** = 内容そのもの（要約・検索・insight・戦略・次の一手）。§6.0 の非目標表はすべてここに落ちる。
> - **形式** = その内容が「いつのものか」「まだ真か」「どこにあるか」という指標。

この定式は loss と invalidation を選言なしに覆う。両方とも「質料は Agent 自身のもの、欠けているのは
形式」だからである。**H1 の因子分解が P0–P6 の順序を決めたように、H2 の形式/質料はこの層の境界判定を決めうる。**

**ただし境界は完全に鋭くはない。正直に言う。** 「seq 41 で読んだこのパスは現在異なる」は Agent が
持っていなかった情報でもある。運用上の線は次に置く:

> 供給してよい形式とは、**Agent 自身の行為と、その観測可能な effect に対する指標**に限る。
> Agent が自らの行為から産出しえなかった内容や解釈は質料であり、供給しない。

「あなたが seq 41 で読んだパスは現在異なる」は前者（自らの read への索引）。
「何がどう変わり、なぜ重要かの要約」は後者。§6.2 が `available_recovery` に既存 affordance の列挙のみを
許し、戦略を禁じているのと同じ線である。

**§6.0 の本文を形式/質料で書き直すかは、ここでは決めない。** ADR 0003 が当該箇所を確定させた直後であり、
未検証の仮説で確定文を書き換えるのは本プロジェクトの手順に反する。**ADR 0005（未執筆）で、H2 の検証結果を
見てから決める。**

### 15.3 THESIS §3(2) の修正

`docs/THESIS.md` §3(2) は現在こう述べている:

> The agent is not incapable of reasoning about its own process. It knew, at time *T*, what it
> had tried... By time *T+n* it has **lost the reference, not the capability**.

**H2 が正しければ、この診断は正しいが浅い。** 参照先を失ったのではなく、**参照先が時間指標を帯びる
ための形式を初めから持っていない**。倉庫から漏れたのではなく、漏れるべき内感が無い。

帰結は実務的である。外部化は「Agent が持っていたものを戻す」作業ではなく、**Agent が持たない形式を
義肢として供給する**作業になる。そして供給は Agent が既に持っている語彙 —— ニュートン的な timestamp と
座標 —— で行えばよい。**harness が新しい時間存在論を発明する必要はない。L5 の ledger が既にその素材である。**

> **設計規則**: *harness は形式を供給する。概念は Agent が既に持っている。*

**THESIS.md（および `docs/ja/THESIS.ja.md`）の §3(2) の書き換えは、H2 の検証後に行う。**
未検証の仮説で thesis を書き換えることはしない —— H1 自身が「証明済みの法則ではない」と明示されている
のと同じ規律である。

### 15.4 危険 —— 現象学を実装し始めないこと

この節の語彙は、これまでで最も scope creep を誘発しやすい。「AI に時間感覚を与える」は巨大な新規
サブシステムへ一直線に繋がる言い回しである。§6.0 の命名規律（「Meta-State Infrastructure と呼ぶ。
Meta-Cognition ではない。**名前が設計を守る**」）をここで全力で適用する。

1. **H2 は診断であって設計ではない。** 症状を束ね、どこに次の欠落があるかを予測する道具である。
   要求する実装は §14 が既に列挙したもの（`estimate`、`stale_read`、topology）を超えない。
   **H2 から新しい層は出てこない。出てきたらそれは scope creep である。**
2. **機械の内的経験について何も主張しない。** 「Agent が時間を経験する」とは言わない。言えるのは
   「時間指標を帯びた状態を外部に置くと行動が変わるか」だけであり、それは測定可能な問いである。
3. **哲学的枠組みは反証不能な主張を密輸しやすい。** ゆえに H2 は H1 と同じ強さで反証条件を持つ
   （§15.0）。持てなくなった時点で、この節は設計文書から削除されるべきポエムである。

### 15.5 検証

§9.1 の paired-session A/B の機構がそのまま使える。**形式の供給と、指示による喚起を分離することが要点**である。

| 条件 | 内容 |
|---|---|
| **対照** | 何も供給しない |
| **形式供給群** | `estimate` の過去実績・`stale_read`・elapsed を構造化された事実として利用可能にする（pull + 閾値 push） |
| **指示群** | 同じ内容を prompt 指示として与える（「時間経過とファイルの変更に注意せよ」） |

- **H2 を支持する結果**: 形式供給群 > 指示群 ≈ 対照。
- **H2 を反証する結果 (1)**: 指示群 ≈ 形式供給群。→ 環境の制御が Agent の制御に勝っていない。L7 縮退。
- **H2 を反証する結果 (2)**: 形式供給群 ≈ 対照。→ 概念を持っているという前提が偽。人間向け記録に縮退。

測定指標は既存のもので足りる: `estimate_ratio`（§9）、`rediscovery_cost`、`recurrence_rate`、
`strategy_revision_rate`。**新しい指標を追加しないことが、H2 が診断にとどまっていることの証拠になる。**
