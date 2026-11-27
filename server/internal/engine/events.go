package engine

// Audience は Event を誰に届けてよいか。
// **イベントも隠し情報を運ぶ**ので、宛先を必ず明示する(既定値はない)。
type Audience struct {
	both bool
	only PlayerID
}

// ToBoth は両プレイヤー宛て。
var ToBoth = Audience{both: true}

// To は指定したプレイヤーだけ宛て。
func To(p PlayerID) Audience { return Audience{only: p} }

// Includes はそのプレイヤーに届けてよいか。
func (a Audience) Includes(p PlayerID) bool { return a.both || a.only == p }

// Event はエンジンが出す「起きたこと」。
//
// 位置 (Pos) はエンジン内部の絶対座標で入っている。クライアントへ送る層は
// 必ず RelativePos でプレイヤー視点に直すこと。状態の全体像は別途 ViewFor で送る。
type Event struct {
	To   Audience
	Body EventBody
}

// EventBody は Event の中身。下の型のどれか。
type EventBody interface{ isEventBody() }

// JankenReason はじゃんけんの用途。
type JankenReason int

const (
	JankenInitiative JankenReason = iota + 1 // 先攻・後攻決め
	JankenContest                            // 予約被り
	JankenTiebreak                           // 所持金同額の決着
)

// RoundStarted は round の開始。先攻が決まった直後に出る。
type RoundStarted struct {
	Round int
	First PlayerID
	// Burst はこの round で先攻トークンが発動したか。
	Burst bool
}

// JankenResolved はじゃんけんの結果。裁定はサーバーが行う。
type JankenResolved struct {
	Reason JankenReason
	Winner PlayerID
	Relic  RelicID // JankenContest のときだけ
}

// TurnStarted はターンの開始。
type TurnStarted struct {
	Player PlayerID
}

// ReservationAccepted は取引予約を受け付けた。**予約した本人にだけ送る。**
type ReservationAccepted struct {
	Relic RelicID
	// EstimatedCost は受付時点の見込み支払額。実際の額は解決時に決まる。
	EstimatedCost int
}

// ReservationResolved は予約が解決して呪物が自陣に置かれた。
type ReservationResolved struct {
	Relic  RelicID
	Winner PlayerID
	// Contested は予約が被ってじゃんけんで決まったか。
	// 先取権(#17)で勝ち切った場合は false。
	Contested bool
	Cost      int
	To        Pos
}

// ReservationLost は予約が被って負けた。**負けた本人にだけ送る。**
type ReservationLost struct {
	Relic RelicID
}

// ReservationFailed は予約が成立しなかった(自陣に空きがない等)。**本人にだけ送る。**
type ReservationFailed struct {
	Relic RelicID
}

// Appraised は鑑定の結果。価値は**両プレイヤー**に公開される。
type Appraised struct {
	Relic RelicID
	By    PlayerID
	Value int
}

// Analyzed は解析の結果。**解析した本人にだけ送る。**
type Analyzed struct {
	Relic RelicID
	// Text が nil = 呪いを持たない呪物だった。
	Text *string
}

// CurseRevealed は呪いの効果文が誰かに露見した(呪い自身による露見。#13 千里眼など)。
// 露見した呪物の持ち主にも届ける(知らないと誤認したまま打ち続けるため)。
type CurseRevealed struct {
	Relic RelicID
	To    PlayerID
}

// AnalyzedByOpponent は自陣の呪物が相手に解析された(#14 逆探知)。**持ち主にだけ送る。**
type AnalyzedByOpponent struct {
	Relic RelicID
}

// Move は drift での1つの呪物の移動。
type Move struct {
	Relic    RelicID
	From, To Pos
}

// Drifted は drift の結果。動いた呪物だけが入る。
type Drifted struct {
	Moves []Move
}

// NoticeKind は通知の種類。
type NoticeKind int

const (
	// NoticePassWarning はパスしようとした。もう一度確認付きで送ればパスになる。
	NoticePassWarning NoticeKind = iota + 1
)

// Notice は警告などの通知。
type Notice struct {
	Kind NoticeKind
}

// GameEnded はゲーム終了。終了時の計上を済ませた最終所持金が入る。
type GameEnded struct {
	FinalMoney      [2]int
	Winner          PlayerID
	SettledByJanken bool
}

func (RoundStarted) isEventBody()        {}
func (JankenResolved) isEventBody()      {}
func (TurnStarted) isEventBody()         {}
func (ReservationAccepted) isEventBody() {}
func (ReservationResolved) isEventBody() {}
func (ReservationLost) isEventBody()     {}
func (ReservationFailed) isEventBody()   {}
func (Appraised) isEventBody()           {}
func (Analyzed) isEventBody()            {}
func (CurseRevealed) isEventBody()       {}
func (AnalyzedByOpponent) isEventBody()  {}
func (Drifted) isEventBody()             {}
func (Notice) isEventBody()              {}
func (GameEnded) isEventBody()           {}
