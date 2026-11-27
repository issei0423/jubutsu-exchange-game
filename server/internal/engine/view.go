package engine

// 射影: 内部状態 → 「そのプレイヤーが見てよい情報だけ」の View。
//
// - View は内部状態とは別の型。内部の構造体をそのまま外へ出さない(不変条件 1)。
// - 隠れうる値はすべてポインタにし、nil = 見えていない とする
//   (proto3 の optional と同じ意味づけ。0 と「隠れている」を区別するため)。
// - 隠し情報の可視性は**必ず visible()(可視性リゾルバ)を通して決める**。
//   ここで if を直接書いて素通りする経路を作らないこと。
// - 座標は視点のプレイヤーから見た相対座標(自陣の奥が Row 0)。

// Zone は視点のプレイヤーから見た領域。
type Zone int

const (
	ZoneOwnField      Zone = iota + 1 // 自陣
	ZoneOpponentField                 // 敵陣
	ZoneFree                          // フリー地
)

// View は1プレイヤーぶんの射影。
type View struct {
	Round       int
	TotalRounds int
	Phase       Phase
	Rows, Cols  int

	YourTurn    bool
	YouAreFirst bool
	// Burst はこの round で先攻トークンが発動しているか。
	Burst bool

	You      PlayerView
	Opponent PlayerView
	// YourActions は自分のターン中だけ設定する。
	YourActions *ActionsView

	Relics []RelicView

	YourReservation *RelicID
	// OpponentReservation は相手の予約。原則 nil(呪い #13 千里眼などで見える)。
	OpponentReservation *RelicID

	// Result は終了後だけ設定する。
	Result *ResultView
}

// PlayerView はプレイヤーの公開情報。
type PlayerView struct {
	// Money が nil = 呪いで隠されている(#11 霧隠れ)。負の値もあり得る。
	Money  *int
	Tokens int
}

// ActionsView はこのターンに残っている行動回数。
type ActionsView struct {
	Trade, Appraise, Analyze int
}

// RelicView は呪物1つぶん。
type RelicView struct {
	ID   RelicID
	Zone Zone
	Pos  Pos // 視点から見た相対座標
	// Value が nil = まだ価値が見えていない(未鑑定)。
	Value *int
	// CurseKnown は効果文を知っているか。CurseKnown かつ CurseText が nil
	// = 「呪いを持たない呪物だと分かった」。
	CurseKnown bool
	CurseText  *string
}

// ResultView は終了結果。
type ResultView struct {
	YourMoney, OpponentMoney int
	YouWon                   bool
	SettledByJanken          bool
}

// RelativePos は絶対座標を viewer から見た座標へ直す。
// 卓を挟んで向かい合うので、プレイヤー1から見ると盤は 180 度回転して見える。
func (g *Game) RelativePos(viewer PlayerID, p Pos) Pos {
	if viewer == 0 {
		return p
	}
	return Pos{Row: g.p.Rows - 1 - p.Row, Col: g.p.Cols - 1 - p.Col}
}

func (g *Game) relativeZone(viewer PlayerID, p Pos) Zone {
	owner, inField := g.fieldOwner(p)
	switch {
	case !inField:
		return ZoneFree
	case owner == viewer:
		return ZoneOwnField
	}
	return ZoneOpponentField
}

// ViewFor は viewer に送ってよい状態だけを作る。
func (g *Game) ViewFor(viewer PlayerID) View {
	opp := viewer.Opponent()
	v := View{
		Round:       g.round,
		TotalRounds: g.p.TotalRounds,
		Phase:       g.phase,
		Rows:        g.p.Rows,
		Cols:        g.p.Cols,
		YourTurn:    g.phase == PhaseTurn && g.active == viewer,
		YouAreFirst: g.first == viewer,
		Burst:       g.burst,
		You:         g.playerView(viewer, viewer),
		Opponent:    g.playerView(viewer, opp),
	}
	if v.YourTurn {
		v.YourActions = &ActionsView{Trade: g.actions.trade, Appraise: g.actions.appraise, Analyze: g.actions.analyze}
	}
	v.YourReservation = g.reservationView(viewer, viewer)
	v.OpponentReservation = g.reservationView(viewer, opp)

	for _, id := range g.ids {
		r := g.relics[id]
		rv := RelicView{
			ID:   id,
			Zone: g.relativeZone(viewer, r.pos),
			Pos:  g.RelativePos(viewer, r.pos),
		}
		if g.visible(VisQuery{Kind: VisRelicValue, Viewer: viewer, Relic: id}) {
			rv.Value = ptr(r.value)
		}
		if g.visible(VisQuery{Kind: VisCurseText, Viewer: viewer, Relic: id}) {
			rv.CurseKnown = true
			if r.curse != nil {
				rv.CurseText = ptr(r.curse.Text)
			}
		}
		v.Relics = append(v.Relics, rv)
	}

	if g.phase == PhaseResult {
		// 終了後は最終所持金を両者に公開する(勝敗の根拠なので隠しようがない)。
		v.Result = &ResultView{
			YourMoney:       g.players[viewer].money,
			OpponentMoney:   g.players[opp].money,
			YouWon:          g.winner == viewer,
			SettledByJanken: g.settledByJanken,
		}
	}
	return v
}

func (g *Game) playerView(viewer, subject PlayerID) PlayerView {
	pv := PlayerView{Tokens: g.players[subject].tokens}
	if g.visible(VisQuery{Kind: VisMoney, Viewer: viewer, Subject: subject}) {
		pv.Money = ptr(g.players[subject].money)
	}
	return pv
}

func (g *Game) reservationView(viewer, subject PlayerID) *RelicID {
	id := g.reserved[subject]
	if id == "" {
		return nil
	}
	if !g.visible(VisQuery{Kind: VisReservation, Viewer: viewer, Subject: subject}) {
		return nil
	}
	return ptr(id)
}

func ptr[T any](v T) *T { return &v }
