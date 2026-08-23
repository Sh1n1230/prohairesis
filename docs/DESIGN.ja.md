> **この文書について**
> 本プロジェクトの設計記録であり、**生きた文書**として維持されます（実装に合わせて更新されます）。
> 承認済みの判断が後の検証で覆った箇所には、それを置き換えた ADR へのリンクを併記しています。
> 設計判断を変更する場合は、この文書を黙って書き換えるのではなく `docs/adr/` に ADR を追加してください。
>
> **実装進捗**: P0（baseline 計測・正直さの文書化）と P1（可逆性基盤）は完了。P2 以降は未着手。
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

- meta-state の物理単位は **session**（`~/.harness/sessions/<id>/state.json`）
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
/Users/shin1230/Git/harness/
  install / uninstall              # POSIX sh。冪等・--dry-run
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
~/.harness/
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

- **`harness.event.v1`** — `{ts, session_id, seq, actor, action{kind,paths,argv,host}, boundary_class, decision{outcome, enforcement, reason}, checkpoint_ref, duration_ms}`。
  `enforcement: kernel | advisory | record-only` —— **このフィールドが §12 の正直さを文書ではなく構造にする。**
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
    [user] ~/.harness/ 作成 / ~/.claude/settings.json に harness ブロック追加(backup 付)
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
| **P0** ✅**完了** | 既存 transcript から friction baseline 取得（`recurrence_rate`/`resignation_rate` の近似も含む）。**`ENFORCEMENT-HONESTY.md` を enforcement コードより先に書く** | `~/.harness/metrics/baseline.json` に §9 の指標が実データで入る |
| **P1** 可逆性 ✅**完了** | repo 外 object store の checkpoint、`protect` パスの clonefile snapshot、`undo`/`diff`。**policy も hook も deny も無し** | scenario test: `rm -rf src && git clean -xdf && echo garbage > pyproject.toml` → `undo` → tracked/untracked/precious が **byte 一致**。加えて `git log`/`status`/`stash list`/reflog が control repo と **byte 一致**。**この時点で FS 操作の prompt を切ってよい。これだけで本プロジェクトは正当化される** |
| **P2** 観測 + 計測 | SessionStart/End/PostToolUse shim（全て record-only・`\|\| true`・絶対に block しない）、JSONL sink、`report`/`metrics` | 実 session が valid な `events.jsonl` を生む。hook 実測 overhead p50 < 20ms（それ自体もログ） |
| **P3** verification | 発見（`run_quality_checks.sh`/Makefile/package.json/cargo）→ 正規化、`harness verify`、**failure fingerprint**、有界な context 注入（ポインタ1行のみ） | `~/signate/_template` で `verify --json` が正規化 JSON を返す。注入 context < 50 token。transcript 上で Agent が自発的に `verify` を呼び finding に対処 |
| **P4a** L7 derived | attempt 導出・elapsed・`last_green`・**recurrence 検出と structured signal**・topology（観測のみ）・`state show`/`timeline`・**signal 抑制フラグ（A/B 対照用）** | 3回同一失敗する仕込み repo で `recurrence` signal が count=3・`last_green` 付きで発火する。**Agent 側の協力ゼロで成立すること**（`hypothesis` を一度も呼ばない session でも全部動く）。signal に戦略文言が1つも含まれないことを golden test で固定。**paired-session A/B（signal 抑制あり/なし）で `strategy_revision_rate` の差を測定できること** —— 差が出なければ L7 の signal 部分は効いておらず、再設計対象 |
| **P4b** L7 declared | `hypothesis set/reject`・`goal`・session lineage と carry-over | 新 session 開始時に前 session の `rejected` が carry-over 候補として提示され、**自動 merge されない**。`reject` が `reason` と `evidence` 無しでは記録を拒否する |
| **P5** policy + 天井 + credential | B0–B6 分類表、`policy compile` → settings.json ブロック + managed-settings.json + sandbox。`block-secrets.sh` 退役、`Run()` 修正、`doctor` | golden test。交わり性の証明（広げる repo policy が理由付きで drop）。`Bash(curl:*)` を allow する red-team fixture repo が `trust` まで無効。**かつ `metrics` が P4 比で `interrupts_per_session` を増やしていないこと。増えていたら出荷しない** |
| **P6** adapter #2 | 最小の custom API-loop adapter（C2 が権威的） | 同一 policy が両 adapter で compile され共通シナリオで等価な決定。**core に `claude` の文字列が0件** |

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
