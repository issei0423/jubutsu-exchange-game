// Package engine は呪物交換対戦ゲームのルールエンジン。
//
//   - 状態はすべてこのパッケージの非公開フィールドに持つ。外へ出すのは
//     ViewFor(プレイヤー視点への射影)と Event だけ。
//     (非公開フィールドなので、うっかり json.Marshal しても中身は出ない。)
//   - I/O を持たない。1対戦 = 1 goroutine がこの Game を専有し、Apply を順に呼ぶ前提。
//     スレッドセーフではない(CLAUDE.md「Go 側の書き方の指針」)。
//   - 乱数はすべて Game が持つ RNG から引く。シードを固定すれば再現できる。
package engine

import (
	"errors"
	"fmt"
	"sort"
)

// PlayerID は 0 か 1。0 の自陣は Row の小さい側、1 の自陣は Row の大きい側。
type PlayerID int

// Opponent は相手のプレイヤー。
func (p PlayerID) Opponent() PlayerID { return 1 - p }

// RelicID は呪物の識別子。**価値や呪いを推測できる情報を含めない**こと。
type RelicID string

// Pos は盤上の絶対座標。Row はプレイヤー0の端が 0。
type Pos struct {
	Row, Col int
}

// Relic は呪物の初期設定(newGame に渡す用)。
type Relic struct {
	ID    RelicID
	Value int
	Curse CurseID // "" = 呪いなし
	Pos   Pos
}

type relic struct {
	id    RelicID
	value int
	curse *Curse // nil = 呪いなし
	pos   Pos
}

type player struct {
	money  int
	tokens int
}

// Phase は進行状況。
type Phase int

const (
	PhaseTurn   Phase = iota + 1 // どちらかのターン中
	PhaseResult                  // 終了
)

type actionsLeft struct {
	trade, appraise, analyze int
}

// Game は1対戦ぶんの状態。
type Game struct {
	p   Params
	rng RNG
	// janken はじゃんけんの裁定。
	// TODO(未定 #16): プレイヤーが手を出す方式か自動抽選かは未定。
	// 【仮】サーバーが乱数で決める。手を出す方式になったらここを差し替える。
	janken func(JankenReason) PlayerID

	round     int
	phase     Phase
	first     PlayerID
	active    PlayerID
	burst     bool
	lastFirst PlayerID
	players   [2]player
	actions   actionsLeft
	acted     bool // このターンに何か行動したか(パス警告用)

	relics map[RelicID]*relic
	ids    []RelicID // 昇順。呪いを畳み込む順序はこれに従う
	cells  map[Pos]RelicID

	appraised map[RelicID]bool    // 鑑定済み(両者に公開)
	revealed  [2]map[RelicID]bool // プレイヤーごとの「効果文を知っている」フラグ
	reserved  [2]RelicID          // この round の予約。"" = なし

	winner          PlayerID
	settledByJanken bool

	events []Event // Apply 中に積むバッファ
}

// エラー。Apply がエラーを返したときは状態を一切変えていない。
var (
	ErrGameOver      = errors.New("engine: ゲームは終了している")
	ErrNotYourTurn   = errors.New("engine: 手番ではない")
	ErrNoActionLeft  = errors.New("engine: その行動はこのターンにもう使えない")
	ErrUnknownRelic  = errors.New("engine: 存在しない呪物")
	ErrInvalidTarget = errors.New("engine: その呪物は対象にできない")
	ErrUnknownCmd    = errors.New("engine: 不明なコマンド")
)

// New は無作為に呪物を置いた新しい対戦を作り、最初の round を始める。
func New(p Params, seed uint64) (*Game, []Event, error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	rng := NewRNG(seed)
	return newGame(p, rng, randomRelics(p, rng), nil)
}

func randomRelics(p Params, rng RNG) []Relic {
	var free []Pos
	for r := p.FieldDepth; r < p.Rows-p.FieldDepth; r++ {
		for c := 0; c < p.Cols; c++ {
			free = append(free, Pos{r, c})
		}
	}
	// Fisher–Yates で先頭 InitialRelics 個を選ぶ。
	for i := 0; i < p.InitialRelics; i++ {
		j := i + rng.IntN(len(free)-i)
		free[i], free[j] = free[j], free[i]
	}
	out := make([]Relic, p.InitialRelics)
	for i := range out {
		var curse CurseID
		if !percent(rng, p.CurselessPercent) {
			curse = curseOrder[rng.IntN(len(curseOrder))].ID
		}
		out[i] = Relic{
			// ID は通し番号。価値・呪いとは無関係なので漏れない。
			ID:    RelicID(fmt.Sprintf("r%02d", i+1)),
			Value: p.ValueMin + rng.IntN(p.ValueMax-p.ValueMin+1),
			Curse: curse,
			Pos:   free[i],
		}
	}
	return out
}

// newGame は呪物の配置を指定して対戦を作る(テストからも使う)。
// janken が nil なら RNG で裁定する。
func newGame(p Params, rng RNG, relics []Relic, janken func(JankenReason) PlayerID) (*Game, []Event, error) {
	if err := p.validate(); err != nil {
		return nil, nil, err
	}
	g := &Game{
		p:         p,
		rng:       rng,
		relics:    map[RelicID]*relic{},
		cells:     map[Pos]RelicID{},
		appraised: map[RelicID]bool{},
		revealed:  [2]map[RelicID]bool{{}, {}},
	}
	g.janken = janken
	if g.janken == nil {
		g.janken = func(JankenReason) PlayerID { return PlayerID(g.rng.IntN(2)) }
	}
	for i := range g.players {
		g.players[i].money = p.StartingMoney
	}
	for _, r := range relics {
		if _, dup := g.relics[r.ID]; dup || r.ID == "" {
			return nil, nil, fmt.Errorf("engine: 呪物 ID が不正 %q", r.ID)
		}
		if !g.inBounds(r.Pos) {
			return nil, nil, fmt.Errorf("engine: 盤外の呪物 %q", r.ID)
		}
		if _, occ := g.cells[r.Pos]; occ {
			return nil, nil, fmt.Errorf("engine: マスが重なっている %q", r.ID)
		}
		rel := &relic{id: r.ID, value: r.Value, pos: r.Pos}
		if r.Curse != "" {
			c, ok := curseRegistry[r.Curse]
			if !ok {
				return nil, nil, fmt.Errorf("engine: 未知の呪い %q", r.Curse)
			}
			rel.curse = c
		}
		g.relics[r.ID] = rel
		g.cells[r.Pos] = r.ID
		g.ids = append(g.ids, r.ID)
	}
	sort.Slice(g.ids, func(i, j int) bool { return g.ids[i] < g.ids[j] })
	g.startRound()
	return g, g.flush(), nil
}

// ---------------------------------------------------------------------------
// 盤面の幾何
// ---------------------------------------------------------------------------

func (g *Game) inBounds(p Pos) bool {
	return p.Row >= 0 && p.Row < g.p.Rows && p.Col >= 0 && p.Col < g.p.Cols
}

func (g *Game) center() int { return g.p.Rows / 2 }

// fieldOwner はそのマスがどちらの自陣か。フリー地なら ok=false。
func (g *Game) fieldOwner(p Pos) (PlayerID, bool) {
	switch {
	case p.Row < g.p.FieldDepth:
		return 0, true
	case p.Row >= g.p.Rows-g.p.FieldDepth:
		return 1, true
	}
	return 0, false
}

// placementCell は pl の自陣で、呪物を置く空きマスを探す。
//
// 【仮】取引で置く位置の指定方法は要件にない(未定 #9 操作方法)。
// 当面は「元の列に近い列」→「自陣の奥(中央から遠い行)」の順で最初の空きに置く。
func (g *Game) placementCell(pl PlayerID, fromCol int) (Pos, bool) {
	cols := make([]int, g.p.Cols)
	for i := range cols {
		cols[i] = i
	}
	sort.SliceStable(cols, func(i, j int) bool {
		di, dj := abs(cols[i]-fromCol), abs(cols[j]-fromCol)
		if di != dj {
			return di < dj
		}
		return cols[i] < cols[j]
	})
	for _, c := range cols {
		for d := 0; d < g.p.FieldDepth; d++ {
			row := d
			if pl == 1 {
				row = g.p.Rows - 1 - d
			}
			p := Pos{row, c}
			if _, occ := g.cells[p]; !occ {
				return p, true
			}
		}
	}
	return Pos{}, false
}

func (g *Game) moveRelic(r *relic, to Pos) {
	delete(g.cells, r.pos)
	r.pos = to
	g.cells[to] = r.id
}

// ---------------------------------------------------------------------------
// コマンド
// ---------------------------------------------------------------------------

// Command はクライアントからの入力。
type Command interface{ isCommand() }

// Trade は取引(予約)。その場では置かれず、round 末にまとめて解決される。
type Trade struct{ Relic RelicID }

// Appraise は鑑定。
type Appraise struct{ Relic RelicID }

// Analyze は解析。
type Analyze struct{ Relic RelicID }

// EndTurn はターン終了。
// 何も行動せずに終えようとするとパス警告を返して止まる。ConfirmPass を立てて
// もう一度送ればパスとして終了する(要件「パスは可能だが警告が出る」)。
type EndTurn struct{ ConfirmPass bool }

func (Trade) isCommand()    {}
func (Appraise) isCommand() {}
func (Analyze) isCommand()  {}
func (EndTurn) isCommand()  {}

// Apply はプレイヤー by のコマンドを処理し、起きたことを返す。
// エラーのときは状態を変えない。
func (g *Game) Apply(by PlayerID, cmd Command) ([]Event, error) {
	if g.phase == PhaseResult {
		return nil, ErrGameOver
	}
	if by != g.active {
		return nil, ErrNotYourTurn
	}
	var err error
	switch c := cmd.(type) {
	case Trade:
		err = g.trade(by, c.Relic)
	case Appraise:
		err = g.appraise(by, c.Relic)
	case Analyze:
		err = g.analyze(by, c.Relic)
	case EndTurn:
		g.endTurn(by, c.ConfirmPass)
	default:
		err = ErrUnknownCmd
	}
	ev := g.flush()
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func (g *Game) emit(to Audience, body EventBody) {
	g.events = append(g.events, Event{To: to, Body: body})
}

func (g *Game) flush() []Event {
	ev := g.events
	g.events = nil
	return ev
}

func (g *Game) trade(by PlayerID, id RelicID) error {
	r, ok := g.relics[id]
	if !ok {
		return ErrUnknownRelic
	}
	if g.actions.trade <= 0 {
		return ErrNoActionLeft
	}
	if owner, inField := g.fieldOwner(r.pos); inField && owner == by {
		return ErrInvalidTarget // 自陣の呪物は取引できない
	}
	g.actions.trade--
	g.acted = true
	g.reserved[by] = id
	g.emit(To(by), ReservationAccepted{Relic: id, EstimatedCost: g.tradeCost(by, r)})
	return nil
}

func (g *Game) appraise(by PlayerID, id RelicID) error {
	r, ok := g.relics[id]
	if !ok {
		return ErrUnknownRelic
	}
	if g.actions.appraise <= 0 {
		return ErrNoActionLeft
	}
	g.actions.appraise--
	g.acted = true
	g.appraised[id] = true
	g.emit(ToBoth, Appraised{Relic: id, By: by, Value: r.value})
	if c, ok := g.activeCurseOf(r); ok && c.def.OnAppraised != nil {
		c.def.OnAppraised(c, by)
	}
	return nil
}

func (g *Game) analyze(by PlayerID, id RelicID) error {
	r, ok := g.relics[id]
	if !ok {
		return ErrUnknownRelic
	}
	if g.actions.analyze <= 0 {
		return ErrNoActionLeft
	}
	g.actions.analyze--
	g.acted = true
	g.revealed[by][id] = true
	var text *string
	if r.curse != nil {
		t := r.curse.Text
		text = &t
	}
	g.emit(To(by), Analyzed{Relic: id, Text: text})
	if c, ok := g.activeCurseOf(r); ok && c.def.OnAnalyzed != nil {
		c.def.OnAnalyzed(c, by)
	}
	return nil
}

func (g *Game) endTurn(by PlayerID, confirmPass bool) {
	if !g.acted && !confirmPass {
		g.emit(To(by), Notice{Kind: NoticePassWarning})
		return
	}
	if g.active == g.first {
		g.startTurn(g.first.Opponent())
		return
	}
	g.endRound()
}

// ---------------------------------------------------------------------------
// round の進行: 先攻ターン → 後攻ターン → 予約解決 → drift → round 終了
// ---------------------------------------------------------------------------

func (g *Game) startRound() {
	g.round++
	g.phase = PhaseTurn
	g.reserved = [2]RelicID{}

	first := g.janken(JankenInitiative)
	g.emit(ToBoth, JankenResolved{Reason: JankenInitiative, Winner: first})

	// 先攻トークン: 連続先攻で1つずつ貯まり、閾値で発動してリセット。
	// 連続が途切れたら(= 相手が先攻を取ったら)リセット。
	if g.round > 1 && first == g.lastFirst {
		g.players[first].tokens++
	} else {
		g.players[first.Opponent()].tokens = 0
		g.players[first].tokens = 1
	}
	g.burst = g.players[first].tokens >= g.p.TokenThreshold
	if g.burst {
		g.players[first].tokens = 0
	}
	g.first = first
	g.lastFirst = first

	g.emit(ToBoth, RoundStarted{Round: g.round, First: first, Burst: g.burst})
	g.startTurn(first)
}

func (g *Game) startTurn(pl PlayerID) {
	g.active = pl
	g.acted = false
	g.actions = actionsLeft{trade: 1, appraise: 1, analyze: 1}
	if g.burst {
		// トークン発動 round: 先攻は鑑定・解析が封じられ、後攻は各2回。
		if pl == g.first {
			g.actions.appraise, g.actions.analyze = 0, 0
		} else {
			g.actions.appraise, g.actions.analyze = 2, 2
		}
	}
	g.emit(ToBoth, TurnStarted{Player: pl})
}

func (g *Game) endRound() {
	g.resolveReservations()
	g.drift()
	// 【仮】onRoundEnd は drift の後に呼ぶ(= drift で自陣から流れ出た呪物の
	// 蝕み・実りはその round には効かない)。要件は「round ごとに」のみ。
	for _, c := range g.activeCurses() {
		if c.def.OnRoundEnd != nil {
			c.def.OnRoundEnd(c)
		}
	}
	if g.round >= g.p.TotalRounds {
		g.endGame()
		return
	}
	g.startRound()
}

func (g *Game) resolveReservations() {
	// 1) 予約に反応する呪い(#13 千里眼の発覚判定)。置く前に判定する:
	//    覗いていたのはターン中なので、この解決で置かれた呪物は対象外。
	for _, c := range g.activeCurses() {
		if c.def.OnReserveResolve != nil {
			c.def.OnReserveResolve(c, g.reserved[c.owner.Opponent()] != "")
		}
	}

	// 2) 予約を呪物ごとにまとめる。呪物 ID 昇順で処理する(再現性のため)。
	claims := map[RelicID][]PlayerID{}
	for pl, id := range g.reserved {
		if id != "" {
			claims[id] = append(claims[id], PlayerID(pl))
		}
	}
	var targets []RelicID
	for id := range claims {
		targets = append(targets, id)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })

	for _, id := range targets {
		who := claims[id]
		winner, contested := who[0], false
		if len(who) == 2 {
			p0, p1 := g.hasPriority(0), g.hasPriority(1)
			switch {
			case p0 && !p1:
				winner = 0
			case p1 && !p0:
				winner = 1
			default:
				winner, contested = g.janken(JankenContest), true
				g.emit(ToBoth, JankenResolved{Reason: JankenContest, Winner: winner, Relic: id})
			}
			g.emit(To(winner.Opponent()), ReservationLost{Relic: id})
		}
		g.place(winner, g.relics[id], contested)
	}
	g.reserved = [2]RelicID{}
}

// hasPriority は pl の自陣に「予約被りを無条件で勝ち切る」呪い(#17)が効いているか。
func (g *Game) hasPriority(pl PlayerID) bool {
	for _, c := range g.activeCurses() {
		if c.owner == pl && c.def.WinsContest {
			return true
		}
	}
	return false
}

func (g *Game) place(pl PlayerID, r *relic, contested bool) {
	if owner, inField := g.fieldOwner(r.pos); inField && owner == pl {
		// 解決の途中で状況が変わって自陣に来ていた(現状は起きないが防御的に)。
		g.emit(To(pl), ReservationFailed{Relic: r.id})
		return
	}
	to, ok := g.placementCell(pl, r.pos.Col)
	if !ok {
		// 【仮】自陣が満杯なら予約は不成立。支払いも発生しない。
		g.emit(To(pl), ReservationFailed{Relic: r.id})
		return
	}
	// 支払額は置く前(= 呪物がまだ元の陣にあり、呪いが効いている状態)で決める。
	cost := g.tradeCost(pl, r)
	if prev, inField := g.fieldOwner(r.pos); inField && prev != pl {
		// 敵陣の呪物の支払いは相手の所持金に入る。所持金はマイナスもあり得る。
		g.players[pl].money -= cost
		g.players[prev].money += cost
	}
	g.moveRelic(r, to)
	g.emit(ToBoth, ReservationResolved{Relic: r.id, Winner: pl, Contested: contested, Cost: cost, To: to})
	if c, ok := g.activeCurseOf(r); ok && c.def.OnPlace != nil {
		c.def.OnPlace(c)
	}
}

// drift は盤上のすべての呪物を中央へ寄せる。
//
// 決め打ちの順序(根拠):
//   - 中央に近い呪物から先に動かす。前が空いてから後ろが詰めるので、
//     「同じ列で一斉に1マス進む」直感と一致する。
//   - 距離が同じなら呪物 ID 昇順。両側から中央の同じマスを取り合うときの
//     勝ち負けをこれで固定する(再現性のため。公平性の調整は要検討)。
//   - 移動先が埋まっていれば、その手前で止まる(【仮】1マス1呪物とする)。
func (g *Game) drift() {
	type plan struct {
		r     *relic
		steps int
	}
	var plans []plan
	for _, id := range g.ids {
		r := g.relics[id]
		steps := 1
		if c, ok := g.activeCurseOf(r); ok && c.def.DriftSteps != nil {
			steps = c.def.DriftSteps(c, steps)
		}
		plans = append(plans, plan{r, steps})
	}
	ctr := g.center()
	sort.SliceStable(plans, func(i, j int) bool {
		di, dj := abs(plans[i].r.pos.Row-ctr), abs(plans[j].r.pos.Row-ctr)
		if di != dj {
			return di < dj
		}
		return plans[i].r.id < plans[j].r.id
	})

	var moves []Move
	for _, pl := range plans {
		r := pl.r
		from := r.pos
		dir := sign(ctr - from.Row) // 中央へ向かう向き。中央にいれば 0
		var target int
		switch {
		case pl.steps >= 0:
			target = from.Row + dir*min(pl.steps, abs(ctr-from.Row))
		case dir == 0:
			target = from.Row // 中央にいる呪物の「逆流」は向きが決まらないので動かない
		default:
			target = clamp(from.Row-dir*(-pl.steps), 0, g.p.Rows-1)
		}
		step := sign(target - from.Row)
		cur := from
		for cur.Row != target {
			next := Pos{cur.Row + step, cur.Col}
			if _, occ := g.cells[next]; occ {
				break
			}
			cur = next
		}
		if cur != from {
			g.moveRelic(r, cur)
			moves = append(moves, Move{Relic: r.id, From: from, To: cur})
		}
	}
	sort.Slice(moves, func(i, j int) bool { return moves[i].Relic < moves[j].Relic })
	g.emit(ToBoth, Drifted{Moves: moves})
}

func (g *Game) endGame() {
	// 1) 終了時の呪い(#6 遺恨)。計上の前に呼ぶが、どちらも足し引きなので順序で結果は変わらない。
	for _, c := range g.activeCurses() {
		if c.def.OnGameEnd != nil {
			c.def.OnGameEnd(c)
		}
	}
	// 2) 価値の計上: 自陣にある呪物の価値を所持金へ加算する(終了時計上)。
	for _, id := range g.ids {
		r := g.relics[id]
		if owner, ok := g.fieldOwner(r.pos); ok {
			g.players[owner].money += r.value
		}
	}
	m0, m1 := g.players[0].money, g.players[1].money
	switch {
	case m0 > m1:
		g.winner = 0
	case m1 > m0:
		g.winner = 1
	default:
		g.winner = g.janken(JankenTiebreak)
		g.settledByJanken = true
		g.emit(ToBoth, JankenResolved{Reason: JankenTiebreak, Winner: g.winner})
	}
	g.phase = PhaseResult
	g.emit(ToBoth, GameEnded{FinalMoney: [2]int{m0, m1}, Winner: g.winner, SettledByJanken: g.settledByJanken})
}

// ---------------------------------------------------------------------------

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func sign(x int) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}

func clamp(x, lo, hi int) int { return max(lo, min(hi, x)) }
