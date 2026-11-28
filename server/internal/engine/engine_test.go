package engine

import (
	"encoding/json"
	"strings"
	"testing"
)

// 盤: 7行 × 3列。自陣 = Row 0,1 (P0) / Row 5,6 (P1)。フリー = Row 2,3,4。中央 = Row 3。
func testParams() Params {
	p := DefaultParams()
	p.Rows = 7
	p.FieldDepth = 2
	p.Cols = 3
	p.InitialRelics = 0
	p.TotalRounds = 3
	return p
}

// scripted はじゃんけんの勝者を順に返す。尽きたら 0。
type scripted struct{ winners []PlayerID }

func (s *scripted) next(JankenReason) PlayerID {
	if len(s.winners) == 0 {
		return 0
	}
	w := s.winners[0]
	s.winners = s.winners[1:]
	return w
}

func setup(t *testing.T, p Params, relics []Relic, winners ...PlayerID) *Game {
	t.Helper()
	s := &scripted{winners: winners}
	g, _, err := newGame(p, NewRNG(1), relics, s.next)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func must(t *testing.T, g *Game, by PlayerID, cmd Command) []Event {
	t.Helper()
	ev, err := g.Apply(by, cmd)
	if err != nil {
		t.Fatalf("Apply(%d, %#v): %v", by, cmd, err)
	}
	return ev
}

// endRound は先攻→後攻の順にターンを終える(パス確認付き)。
func endRound(t *testing.T, g *Game) {
	t.Helper()
	must(t, g, g.first, EndTurn{ConfirmPass: true})
	must(t, g, g.first.Opponent(), EndTurn{ConfirmPass: true})
}

func pos(t *testing.T, g *Game, id RelicID) Pos {
	t.Helper()
	return g.relics[id].pos
}

func find[T EventBody](ev []Event) (T, bool) {
	for _, e := range ev {
		if b, ok := e.Body.(T); ok {
			return b, true
		}
	}
	var zero T
	return zero, false
}

// ---------------------------------------------------------------------------
// 進行
// ---------------------------------------------------------------------------

func TestInitiativeTokens(t *testing.T) {
	p := testParams()
	p.TotalRounds = 10
	// P0 が3連続先攻 → 3 round 目で発動。その後 P1 が取って P0 はリセット。
	g := setup(t, p, nil, 0, 0, 0, 0, 1)

	wantTokens := []int{1, 2, 0, 1, 1}
	wantBurst := []bool{false, false, true, false, false}
	wantFirst := []PlayerID{0, 0, 0, 0, 1}
	for i := range wantTokens {
		if g.first != wantFirst[i] || g.players[g.first].tokens != wantTokens[i] || g.burst != wantBurst[i] {
			t.Fatalf("round %d: first=%d tokens=%d burst=%v", g.round, g.first, g.players[g.first].tokens, g.burst)
		}
		if i == len(wantTokens)-1 && g.players[0].tokens != 0 {
			t.Fatalf("連続が途切れたらリセット: P0 tokens=%d", g.players[0].tokens)
		}
		endRound(t, g)
	}
}

func TestBurstActions(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{3, 0}}}, 0, 0, 0)
	endRound(t, g)
	endRound(t, g)
	if !g.burst {
		t.Fatal("3 round 目で発動するはず")
	}
	if _, err := g.Apply(0, Appraise{Relic: "a"}); err != ErrNoActionLeft {
		t.Fatalf("先攻の鑑定は封じられる: %v", err)
	}
	must(t, g, 0, Trade{Relic: "a"}) // 取引は封じられない
	must(t, g, 0, EndTurn{})
	for i := 0; i < 2; i++ {
		must(t, g, 1, Appraise{Relic: "a"})
		must(t, g, 1, Analyze{Relic: "a"})
	}
	if _, err := g.Apply(1, Appraise{Relic: "a"}); err != ErrNoActionLeft {
		t.Fatalf("後攻は2回まで: %v", err)
	}
}

func TestPassWarning(t *testing.T) {
	g := setup(t, testParams(), nil, 0)
	ev := must(t, g, 0, EndTurn{})
	if n, ok := find[Notice](ev); !ok || n.Kind != NoticePassWarning {
		t.Fatal("何もせずに終えると警告が出る")
	}
	if g.active != 0 {
		t.Fatal("警告だけではターンは終わらない")
	}
	must(t, g, 0, EndTurn{ConfirmPass: true})
	if g.active != 1 {
		t.Fatal("確認付きならパスできる")
	}
}

func TestNotYourTurnDoesNotMutate(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{3, 0}}}, 0)
	if _, err := g.Apply(1, Appraise{Relic: "a"}); err != ErrNotYourTurn {
		t.Fatal(err)
	}
	if g.appraised["a"] {
		t.Fatal("エラー時に状態が変わった")
	}
}

// ---------------------------------------------------------------------------
// 取引
// ---------------------------------------------------------------------------

func TestTradeFromFreeZoneIsReservedThenPlaced(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Value: 10, Pos: Pos{3, 1}}}, 0)
	must(t, g, 0, Trade{Relic: "a"})
	if owner, in := g.fieldOwner(pos(t, g, "a")); in {
		t.Fatalf("予約時点では置かれない(owner=%d)", owner)
	}
	must(t, g, 0, EndTurn{})
	must(t, g, 1, EndTurn{ConfirmPass: true})

	if owner, in := g.fieldOwner(pos(t, g, "a")); !in || owner != 0 {
		t.Fatalf("round 末に P0 の自陣へ置かれる: %v", pos(t, g, "a"))
	}
	if g.players[0].money != g.p.StartingMoney {
		t.Fatal("フリー地の取引はノーコスト。配置で所持金は動かない(終了時計上)")
	}
}

func TestEnemyTradePaysOpponentAndMoneyCanGoNegative(t *testing.T) {
	p := testParams()
	p.StartingMoney = 5
	g := setup(t, p, []Relic{{ID: "a", Pos: Pos{6, 0}}}, 0) // P1 の自陣
	must(t, g, 0, Trade{Relic: "a"})
	endTurns(t, g)
	if g.players[0].money != 5-p.EnemyTradeCost || g.players[1].money != 5+p.EnemyTradeCost {
		t.Fatalf("支払いは相手へ: %d / %d", g.players[0].money, g.players[1].money)
	}
	if g.players[0].money >= 0 {
		t.Fatal("このケースではマイナスになるはず(0 でクランプしない)")
	}
}

func endTurns(t *testing.T, g *Game) {
	t.Helper()
	must(t, g, g.active, EndTurn{ConfirmPass: true})
	must(t, g, g.active, EndTurn{ConfirmPass: true})
}

func TestCannotTradeOwnFieldRelic(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{0, 0}}}, 0)
	if _, err := g.Apply(0, Trade{Relic: "a"}); err != ErrInvalidTarget {
		t.Fatal(err)
	}
}

func TestContestedReservationJanken(t *testing.T) {
	// 先攻=P0, 予約被りじゃんけん=P1 勝ち
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{3, 1}}}, 0, 1)
	must(t, g, 0, Trade{Relic: "a"})
	must(t, g, 0, EndTurn{})
	must(t, g, 1, Trade{Relic: "a"})
	ev := must(t, g, 1, EndTurn{})

	res, ok := find[ReservationResolved](ev)
	if !ok || res.Winner != 1 || !res.Contested {
		t.Fatalf("じゃんけんで P1: %+v", res)
	}
	lost := false
	for _, e := range ev {
		if _, ok := e.Body.(ReservationLost); ok {
			lost = e.To.Includes(0) && !e.To.Includes(1)
		}
	}
	if !lost {
		t.Fatal("負けた側にだけ通知する")
	}
}

func TestPriorityWinsWithoutJanken(t *testing.T) {
	g := setup(t, testParams(), []Relic{
		{ID: "a", Pos: Pos{3, 1}},
		{ID: "p", Curse: "priority", Pos: Pos{0, 0}}, // P0 の自陣に先取権
	}, 0, 1)
	must(t, g, 0, Trade{Relic: "a"})
	must(t, g, 0, EndTurn{})
	must(t, g, 1, Trade{Relic: "a"})
	ev := must(t, g, 1, EndTurn{})
	res, _ := find[ReservationResolved](ev)
	if res.Winner != 0 || res.Contested {
		t.Fatalf("先取権で P0 が無条件に勝つ: %+v", res)
	}
}

// ---------------------------------------------------------------------------
// drift
// ---------------------------------------------------------------------------

func TestDrift(t *testing.T) {
	g := setup(t, testParams(), []Relic{
		{ID: "a", Pos: Pos{0, 0}},                     // P0 自陣 → 1
		{ID: "b", Pos: Pos{3, 1}},                     // 中央で止まる
		{ID: "c", Pos: Pos{6, 2}},                     // P1 自陣 → 5
		{ID: "s", Curse: "stillness", Pos: Pos{1, 1}}, // 静止
		{ID: "r", Curse: "rapids", Pos: Pos{5, 1}},    // 急流 → 2マス進みたいが中央(3,1)は b が居る → 4
		{ID: "x", Curse: "rapids", Pos: Pos{4, 0}},    // フリー地では呪いが眠る → 1マス
		{ID: "k", Curse: "backflow", Pos: Pos{1, 2}},  // 逆流 → 0
	}, 0)
	endRound(t, g)
	want := map[RelicID]Pos{
		"a": {1, 0}, "b": {3, 1}, "c": {5, 2}, "s": {1, 1}, "r": {4, 1}, "x": {3, 0}, "k": {0, 2},
	}
	for id, w := range want {
		if got := pos(t, g, id); got != w {
			t.Errorf("%s: got %v want %v", id, got, w)
		}
	}
}

func TestDriftCenterContestIsDeterministic(t *testing.T) {
	// 両側から中央の同じマスへ。距離が同じなら ID 昇順で先に動いた方が取る。
	g := setup(t, testParams(), []Relic{
		{ID: "b", Pos: Pos{4, 0}},
		{ID: "a", Pos: Pos{2, 0}},
	}, 0)
	endRound(t, g)
	if pos(t, g, "a") != (Pos{3, 0}) || pos(t, g, "b") != (Pos{4, 0}) {
		t.Fatalf("a=%v b=%v", pos(t, g, "a"), pos(t, g, "b"))
	}
}

// ---------------------------------------------------------------------------
// 終了時計上・お金系の呪い
// ---------------------------------------------------------------------------

func TestEndGameScoring(t *testing.T) {
	p := testParams()
	p.TotalRounds = 1
	g := setup(t, p, []Relic{
		{ID: "a", Value: 30, Pos: Pos{0, 0}},                  // P0: +30
		{ID: "g", Value: 10, Curse: "grudge", Pos: Pos{0, 1}}, // P0: +10 してから遺恨 -10
		{ID: "b", Value: 99, Pos: Pos{3, 0}},                  // フリー: 計上されない
	}, 0)
	ev := must(t, g, 0, EndTurn{ConfirmPass: true})
	ev = append(ev, must(t, g, 1, EndTurn{ConfirmPass: true})...)
	end, ok := find[GameEnded](ev)
	if !ok {
		t.Fatal("終了しない")
	}
	if end.FinalMoney[0] != p.StartingMoney+30 || end.FinalMoney[1] != p.StartingMoney || end.Winner != 0 {
		t.Fatalf("%+v", end)
	}
	if _, err := g.Apply(0, EndTurn{}); err != ErrGameOver {
		t.Fatal(err)
	}
}

func TestTieIsSettledByJanken(t *testing.T) {
	p := testParams()
	p.TotalRounds = 1
	g := setup(t, p, nil, 0, 1)
	endRound(t, g)
	if g.winner != 1 || !g.settledByJanken {
		t.Fatal("同額はじゃんけんで決着")
	}
}

func TestErosionAndHarvest(t *testing.T) {
	p := testParams()
	p.TotalRounds = 5
	p.RoundTransferPercent = 50
	g := setup(t, p, []Relic{
		{ID: "e", Value: 20, Curse: "erosion", Pos: Pos{0, 0}}, // P0
		{ID: "h", Value: 20, Curse: "harvest", Pos: Pos{6, 0}}, // P1
	}, 0)
	endRound(t, g) // drift で 1 マス進んでもまだ自陣(Row 1 / Row 5)
	if g.players[0].money != p.StartingMoney-10 || g.relics["e"].value != 30 {
		t.Fatalf("蝕み: money=%d value=%d", g.players[0].money, g.relics["e"].value)
	}
	if g.players[1].money != p.StartingMoney+10 || g.relics["h"].value != 10 {
		t.Fatalf("実り: money=%d value=%d", g.players[1].money, g.relics["h"].value)
	}
}

func TestRansomAndDumping(t *testing.T) {
	g := setup(t, testParams(), []Relic{
		{ID: "r", Curse: "ransom", Pos: Pos{6, 0}},
		{ID: "d", Curse: "dumping", Pos: Pos{6, 1}},
	}, 0)
	if c := g.tradeCost(0, g.relics["r"]); c != g.p.EnemyTradeCost*g.p.RansomMultiplier {
		t.Fatalf("身代金: %d", c)
	}
	if c := g.tradeCost(0, g.relics["d"]); c != 0 {
		t.Fatalf("投げ売り: %d", c)
	}
}

func TestHungerAndCounterTrace(t *testing.T) {
	g := setup(t, testParams(), []Relic{
		{ID: "h", Curse: "hunger", Pos: Pos{6, 0}},
		{ID: "c", Curse: "counterTrace", Pos: Pos{6, 1}},
	}, 0)
	must(t, g, 0, Appraise{Relic: "h"})
	if g.players[1].money != g.p.StartingMoney-g.p.HungerCost {
		t.Fatal("餓え: 調べられると持ち主の所持金が減る")
	}
	ev := must(t, g, 0, Analyze{Relic: "c"})
	ok := false
	for _, e := range ev {
		if _, is := e.Body.(AnalyzedByOpponent); is {
			ok = e.To.Includes(1) && !e.To.Includes(0)
		}
	}
	if !ok {
		t.Fatal("逆探知: 持ち主にだけ通知")
	}
}

// ---------------------------------------------------------------------------
// 可視性 (不変条件 1・2)
// ---------------------------------------------------------------------------

func relicView(v View, id RelicID) RelicView {
	for _, r := range v.Relics {
		if r.ID == id {
			return r
		}
	}
	panic("no relic " + id)
}

func TestAppraiseIsPublicAnalyzeIsPrivate(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Value: 0, Curse: "stillness", Pos: Pos{3, 0}}}, 0)
	for _, pl := range []PlayerID{0, 1} {
		r := relicView(g.ViewFor(pl), "a")
		if r.Value != nil || r.CurseKnown || r.CurseText != nil {
			t.Fatal("未鑑定・未解析は見えない")
		}
	}
	must(t, g, 0, Appraise{Relic: "a"})
	must(t, g, 0, Analyze{Relic: "a"})
	for _, pl := range []PlayerID{0, 1} {
		r := relicView(g.ViewFor(pl), "a")
		if r.Value == nil || *r.Value != 0 {
			t.Fatal("鑑定結果(価値 0 でも)は両者に見える")
		}
	}
	if r := relicView(g.ViewFor(0), "a"); !r.CurseKnown || r.CurseText == nil {
		t.Fatal("解析した側には効果文が見える")
	}
	if r := relicView(g.ViewFor(1), "a"); r.CurseKnown || r.CurseText != nil {
		t.Fatal("解析していない側には見えない")
	}
}

func TestAnalyzeCurselessRelic(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{3, 0}}}, 0)
	must(t, g, 0, Analyze{Relic: "a"})
	if r := relicView(g.ViewFor(0), "a"); !r.CurseKnown || r.CurseText != nil {
		t.Fatal("呪いなしと判明した状態")
	}
}

func TestMistHidesMoney(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "m", Curse: "mist", Pos: Pos{0, 0}}}, 0)
	if g.ViewFor(1).Opponent.Money != nil {
		t.Fatal("霧隠れ: 相手からは見えない")
	}
	if g.ViewFor(0).You.Money == nil {
		t.Fatal("自分には見える")
	}
	if g.ViewFor(0).Opponent.Money == nil {
		t.Fatal("霧隠れの持ち主は相手の所持金を見られる")
	}
}

func TestReservationsHiddenUnlessClairvoyance(t *testing.T) {
	p := testParams()
	p.ClairvoyanceDetectPercent = 100
	g := setup(t, p, []Relic{
		{ID: "a", Pos: Pos{3, 0}},
		{ID: "cv", Curse: "clairvoyance", Pos: Pos{6, 0}}, // P1 の自陣
	}, 0)
	must(t, g, 0, Trade{Relic: "a"})
	if g.ViewFor(0).YourReservation == nil {
		t.Fatal("自分の予約は見える")
	}
	if got := g.ViewFor(1).OpponentReservation; got == nil || *got != "a" {
		t.Fatal("千里眼の持ち主には相手の予約が見える")
	}

	g2 := setup(t, p, []Relic{{ID: "a", Pos: Pos{3, 0}}}, 0)
	must(t, g2, 0, Trade{Relic: "a"})
	if g2.ViewFor(1).OpponentReservation != nil {
		t.Fatal("通常、相手の予約は見えない")
	}

	// 発覚(確率 100%): 予約した P0 に千里眼が露見し、両者に通知される。
	must(t, g, 0, EndTurn{})
	ev := must(t, g, 1, EndTurn{ConfirmPass: true})
	cr, ok := find[CurseRevealed](ev)
	if !ok || cr.To != 0 {
		t.Fatal("千里眼が露見する")
	}
	if r := relicView(g.ViewFor(0), "cv"); !r.CurseKnown {
		t.Fatal("露見後は解析済みと同じ状態")
	}
}

// TestNoLeakInSerializedView は不変条件 1 の番人。
// 何も調べていない相手の View を JSON にしても、価値・効果文・呪いの種類が出てこないこと。
func TestNoLeakInSerializedView(t *testing.T) {
	g := setup(t, testParams(), []Relic{
		{ID: "a", Value: 4242, Curse: "clairvoyance", Pos: Pos{3, 0}},
	}, 0)
	b, err := json.Marshal(g.ViewFor(1))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, secret := range []string{"4242", "clairvoyance", "千里眼", curseRegistry["clairvoyance"].Text} {
		if strings.Contains(s, secret) {
			t.Fatalf("隠し情報 %q が View に漏れている: %s", secret, s)
		}
	}
	// 内部状態をそのまま marshal しても中身は出ない(非公開フィールド)。
	if raw, _ := json.Marshal(g); strings.Contains(string(raw), "4242") {
		t.Fatal("Game を marshal すると隠し情報が出る")
	}
}

func TestRelativePosRotates(t *testing.T) {
	g := setup(t, testParams(), []Relic{{ID: "a", Pos: Pos{6, 2}}}, 0)
	r := relicView(g.ViewFor(1), "a")
	if r.Pos != (Pos{0, 0}) || r.Zone != ZoneOwnField {
		t.Fatalf("P1 から見ると自陣の奥: %+v", r)
	}
	if r := relicView(g.ViewFor(0), "a"); r.Zone != ZoneOpponentField {
		t.Fatal("P0 から見ると敵陣")
	}
}

// ---------------------------------------------------------------------------
// 再現性: 同じシード・同じ入力なら同じ結果
// ---------------------------------------------------------------------------

func playRandom(t *testing.T, seed uint64) [2]int {
	t.Helper()
	g, _, err := New(DefaultParams(), seed)
	if err != nil {
		t.Fatal(err)
	}
	inputs := NewRNG(seed + 1000)
	for g.phase != PhaseResult {
		pl := g.active
		v := g.ViewFor(pl)
		pick := v.Relics[inputs.IntN(len(v.Relics))].ID
		_, _ = g.Apply(pl, Trade{Relic: pick}) // 自陣の呪物ならエラーで無視
		_, _ = g.Apply(pl, Appraise{Relic: pick})
		_, _ = g.Apply(pl, Analyze{Relic: pick})
		must(t, g, pl, EndTurn{ConfirmPass: true})
	}
	return [2]int{g.players[0].money, g.players[1].money}
}

func TestDeterministicReplay(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		if a, b := playRandom(t, seed), playRandom(t, seed); a != b {
			t.Fatalf("seed %d: %v != %v", seed, a, b)
		}
	}
}
