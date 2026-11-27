package engine

// 呪いの定義。CLAUDE.md「呪いの実装方針」に従い、**呪い1つを1定義で完結させる**。
// エンジン本体に呪いごとの分岐を書かないこと。呪いはこの3分類のどれかに載せる:
//
//	(1) イベントフック — 状態を書き換える (On*, DriftSteps)
//	(2) リゾルバ       — 値を決める。状態を変えない (TradeCost, Visibility, WinsContest)
//	(3) 任意発動アビリティ — 未実装(#5 席替え。行動回数の扱いが未決: 呪い案 論点 #2)
//
// 呪いが効くのは、その呪物が**どちらかの自陣にある間だけ**。フリー地では眠っている。
// TODO: 要件「自陣から外れた呪物は呪いが解かれる」が、再び自陣に入ったら
// 効き直すのか(=一時停止)永久に消えるのかは未確認。【仮】一時停止として扱う。
//
// 内容は 呪い案.md(提案・未確定)に基づく。採否が決まったら差し替える。

// CurseID は呪いの種類。**クライアントへ送らない**(種類そのものが隠し情報)。
type CurseID string

// Curse は呪い1種類の定義。使わないフックは nil のままにする。
type Curse struct {
	ID   CurseID
	Name string
	// Text は解析で見える効果文。
	Text string

	// --- (1) イベントフック ---

	OnPlace     func(c *CurseCtx)              // 自陣に置かれた直後
	OnAppraised func(c *CurseCtx, by PlayerID) // 鑑定された直後
	OnAnalyzed  func(c *CurseCtx, by PlayerID) // 解析された直後
	OnRoundEnd  func(c *CurseCtx)              // round 末(drift の後)
	OnGameEnd   func(c *CurseCtx)              // 終了時の計上の前
	// OnReserveResolve は予約解決の直前。opponentReserved は持ち主の相手が
	// この round に予約していたか。
	OnReserveResolve func(c *CurseCtx, opponentReserved bool)
	// DriftSteps は drift で動くマス数を返す。def は既定値(1)。
	// 0 = 動かない、負 = 中央と逆向き(自陣の奥)へ動く。
	DriftSteps func(c *CurseCtx, def int) int

	// --- (2) リゾルバ ---

	// TradeCost は trader がこの呪物を取引するときの支払額への意見。
	TradeCost func(c *CurseCtx, trader PlayerID) CostOpinion
	// Visibility は射影時の可視性への意見。
	Visibility func(c *CurseCtx, q VisQuery) Opinion
	// WinsContest が true なら、持ち主は予約被りを無条件で勝ち切る。
	WinsContest bool
}

// CurseCtx は効いている呪い1つぶんの文脈。
type CurseCtx struct {
	g     *Game
	relic *relic
	// owner はこの呪物が置かれている自陣の持ち主。
	owner PlayerID
	def   *Curse
}

// activeCurseOf は r の呪いが今効いているなら、その文脈を返す。
func (g *Game) activeCurseOf(r *relic) (*CurseCtx, bool) {
	if r.curse == nil {
		return nil, false
	}
	owner, inField := g.fieldOwner(r.pos)
	if !inField {
		return nil, false
	}
	return &CurseCtx{g: g, relic: r, owner: owner, def: r.curse}, true
}

// activeCurses は今効いている呪いを**呪物 ID 昇順**で返す。
// フックやリゾルバの呼び出し順はすべてこれに従う(再現性のため)。
func (g *Game) activeCurses() []*CurseCtx {
	var out []*CurseCtx
	for _, id := range g.ids {
		if c, ok := g.activeCurseOf(g.relics[id]); ok {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 取引価格リゾルバ
// ---------------------------------------------------------------------------

// CostOpinion は支払額への意見。
type CostOpinion struct {
	Free       bool // 支払いなしにする
	Multiplier int  // 0 または 1 = 変えない
}

// tradeCost は trader が r を取引するときの支払額。
//
// 畳み込みの規則(順序に依存しないように決め打ち):
//   - 1つでも Free があれば 0。
//   - そうでなければ 基本額 × 全 Multiplier の積(掛け算なので順不同)。
//
// 基本額: フリー地 0 / 敵陣 EnemyTradeCost(【仮】未定 #6)。
func (g *Game) tradeCost(trader PlayerID, r *relic) int {
	base := 0
	if owner, inField := g.fieldOwner(r.pos); inField && owner != trader {
		base = g.p.EnemyTradeCost
	}
	cost := base
	for _, c := range g.activeCurses() {
		// 現状の呪いはどれも「取引される呪物自身」にだけ効く。
		if c.relic != r || c.def.TradeCost == nil {
			continue
		}
		op := c.def.TradeCost(c, trader)
		if op.Free {
			return 0
		}
		if op.Multiplier > 1 {
			cost *= op.Multiplier
		}
	}
	return cost
}

// ---------------------------------------------------------------------------
// 可視性リゾルバ — 不変条件 1 の要。射影はすべて visible() を通すこと。
// ---------------------------------------------------------------------------

// VisKind は可視性を問う対象の種類。
type VisKind int

const (
	VisRelicValue  VisKind = iota + 1 // 呪物の価値
	VisCurseText                      // 呪いの効果文
	VisMoney                          // プレイヤーの所持金
	VisReservation                    // プレイヤーの取引予約
)

// VisQuery は「Viewer に、この情報を見せるか」。
type VisQuery struct {
	Kind    VisKind
	Viewer  PlayerID
	Subject PlayerID // VisMoney / VisReservation のとき、誰の情報か
	Relic   RelicID  // VisRelicValue / VisCurseText のとき
}

// Opinion は可視性への意見。
type Opinion int

const (
	NoOpinion Opinion = iota
	Show
	Hide
)

// visible は q の答え。
//
// 畳み込みの規則(順序に依存しないように決め打ち):
//
//	1つでも Hide → 見せない / そうでなく1つでも Show → 見せる / 誰も意見なし → 既定。
//
// 根拠: 「隠す」を優先すると、呪い同士が衝突したときに倒れる方向が
// 常に「漏らさない」側になる(不変条件 1)。また順序に依存しないので、
// 呪い同士の評価順を変えても結果が変わらず再現性が保たれる。
func (g *Game) visible(q VisQuery) bool {
	show, hide := false, false
	for _, c := range g.activeCurses() {
		if c.def.Visibility == nil {
			continue
		}
		switch c.def.Visibility(c, q) {
		case Show:
			show = true
		case Hide:
			hide = true
		}
	}
	switch {
	case hide:
		return false
	case show:
		return true
	}
	return g.baseVisible(q)
}

// baseVisible は呪いがないときの既定の可視性(要件定義書 §5)。
func (g *Game) baseVisible(q VisQuery) bool {
	switch q.Kind {
	case VisRelicValue:
		return g.appraised[q.Relic] // 鑑定済みなら両者に見える
	case VisCurseText:
		return g.revealed[q.Viewer][q.Relic] // 解析した側(または露見した相手)だけ
	case VisMoney:
		return true // 所持金は原則相手にも見える
	case VisReservation:
		return q.Viewer == q.Subject // 予約は原則自分のものだけ
	}
	return false // 未知の問いは見せない
}

// ---------------------------------------------------------------------------
// CurseCtx の操作(呪いの中から使う)
// ---------------------------------------------------------------------------

func (c *CurseCtx) addMoney(pl PlayerID, delta int) { c.g.players[pl].money += delta }

// revealTo は呪いの効果文を pl に露見させ、両者に知らせる。
func (c *CurseCtx) revealTo(pl PlayerID) {
	c.g.revealed[pl][c.relic.id] = true
	c.g.emit(ToBoth, CurseRevealed{Relic: c.relic.id, To: pl})
}

// transfer は価値の rate% ぶんを、toValue なら「所持金 → 価値」、そうでなければ「価値 → 所持金」へ移す。
func (c *CurseCtx) transfer(toValue bool) {
	amt := c.relic.value * c.g.p.RoundTransferPercent / 100
	if toValue {
		c.addMoney(c.owner, -amt)
		c.relic.value += amt
	} else {
		c.addMoney(c.owner, amt)
		c.relic.value -= amt
	}
}

// ---------------------------------------------------------------------------
// 呪いの一覧(呪い案.md の番号)
//
// 未実装: #3 道連れ / #5 席替え(任意発動) / #12 封印(対象マスの指定方法が未定)
//         #16 共鳴 / #18 相乗り / #19 呪縛 / #20 忘却
// ---------------------------------------------------------------------------

var curseOrder = []*Curse{
	{ // #1
		ID: "stillness", Name: "静止",
		Text:       "この呪物は流されない。",
		DriftSteps: func(*CurseCtx, int) int { return 0 },
	},
	{ // #2
		ID: "backflow", Name: "逆流",
		Text:       "この呪物は中央と逆へ1マス流される。",
		DriftSteps: func(*CurseCtx, int) int { return -1 },
	},
	{ // #4
		ID: "rapids", Name: "急流",
		Text:       "この呪物は2マス流される。",
		DriftSteps: func(*CurseCtx, int) int { return 2 },
	},
	{ // #6
		ID: "grudge", Name: "遺恨",
		Text:      "ゲーム終了時、自陣にあれば、この呪物の価値ぶん所持金が減る。",
		OnGameEnd: func(c *CurseCtx) { c.addMoney(c.owner, -c.relic.value) },
	},
	{ // #7
		ID: "erosion", Name: "蝕み",
		Text:       "自陣にある間、round ごとに所持金の一部がこの呪物の価値へ移る。",
		OnRoundEnd: func(c *CurseCtx) { c.transfer(true) },
	},
	{ // #8
		ID: "harvest", Name: "実り",
		Text:       "自陣にある間、round ごとにこの呪物の価値の一部が所持金へ移る。",
		OnRoundEnd: func(c *CurseCtx) { c.transfer(false) },
	},
	{ // #9
		ID: "ransom", Name: "身代金",
		Text: "相手がこの呪物を取引するとき、支払額が倍になる。",
		TradeCost: func(c *CurseCtx, trader PlayerID) CostOpinion {
			if trader == c.owner {
				return CostOpinion{}
			}
			return CostOpinion{Multiplier: c.g.p.RansomMultiplier}
		},
	},
	{ // #10
		ID: "dumping", Name: "投げ売り",
		Text: "この呪物は敵陣にあってもノーコストで取引できる。",
		TradeCost: func(c *CurseCtx, trader PlayerID) CostOpinion {
			return CostOpinion{Free: trader != c.owner}
		},
	},
	{ // #11
		ID: "mist", Name: "霧隠れ",
		Text: "自陣にある間、自分の所持金が相手から見えない。",
		Visibility: func(c *CurseCtx, q VisQuery) Opinion {
			if q.Kind == VisMoney && q.Subject == c.owner && q.Viewer != c.owner {
				return Hide
			}
			return NoOpinion
		},
	},
	{ // #13
		ID: "clairvoyance", Name: "千里眼",
		Text: "自陣にある間、相手の取引予約が見える。覗くたびに一定確率で、この呪いが相手に露見する。",
		Visibility: func(c *CurseCtx, q VisQuery) Opinion {
			if q.Kind == VisReservation && q.Viewer == c.owner && q.Subject != c.owner {
				return Show
			}
			return NoOpinion
		},
		// 判定は「相手が予約した round ごとに1回」(呪い案 論点 #1)。発覚後も効果は続く。
		OnReserveResolve: func(c *CurseCtx, opponentReserved bool) {
			opp := c.owner.Opponent()
			if !opponentReserved || c.g.revealed[opp][c.relic.id] {
				return
			}
			if percent(c.g.rng, c.g.p.ClairvoyanceDetectPercent) {
				c.revealTo(opp)
			}
		},
	},
	{ // #14
		ID: "counterTrace", Name: "逆探知",
		Text: "この呪物を相手が解析すると、持ち主に知らされる。",
		OnAnalyzed: func(c *CurseCtx, by PlayerID) {
			if by != c.owner {
				c.g.emit(To(c.owner), AnalyzedByOpponent{Relic: c.relic.id})
			}
		},
	},
	{ // #15
		ID: "hunger", Name: "餓え",
		Text:        "自陣にある間、この呪物が鑑定・解析されるたびに持ち主の所持金が減る。",
		OnAppraised: func(c *CurseCtx, _ PlayerID) { c.addMoney(c.owner, -c.g.p.HungerCost) },
		OnAnalyzed:  func(c *CurseCtx, _ PlayerID) { c.addMoney(c.owner, -c.g.p.HungerCost) },
	},
	{ // #17
		ID: "priority", Name: "先取権",
		Text:        "予約が被ったとき、じゃんけんなしで無条件に勝つ。",
		WinsContest: true,
	},
}

var curseRegistry = func() map[CurseID]*Curse {
	m := map[CurseID]*Curse{}
	for _, c := range curseOrder {
		if _, dup := m[c.ID]; dup {
			panic("engine: 呪い ID の重複 " + string(c.ID))
		}
		m[c.ID] = c
	}
	return m
}()
