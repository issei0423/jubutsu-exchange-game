package engine

import (
	"errors"
	"math/rand/v2"
)

// Params はゲームの数値設定。
//
// **ここにある値はほぼすべて「仮」。** 要件定義書末尾の「未定・要検討リスト」
// (コメントの #番号) と 呪い案.md の論点が決まり次第ここだけを差し替える。
// 未定の値をこのファイル以外に直書きしないこと(CLAUDE.md「ドキュメントの扱い」)。
type Params struct {
	// --- 盤面 (未定 #3) ---

	// Rows は奥行き方向(プレイヤー0の端 → プレイヤー1の端)のマス数。
	// 中央の行で drift が止まるので奇数にする。
	Rows int
	// Cols は横方向のマス数。
	Cols int
	// FieldDepth は自陣の行数(両端からこの行数ぶんが各プレイヤーの自陣)。
	FieldDepth int
	// InitialRelics は開始時の呪物の数。
	// 【仮】開始時はフリー地にだけ無作為に置く(要件は「無作為に置かれている」のみ)。
	InitialRelics int

	// --- お金 ---

	// StartingMoney は初期所持金 (未定 #5)。
	StartingMoney int
	// EnemyTradeCost は敵陣の呪物を取引するときの基本支払額 (未定 #6)。
	// 【仮】決め方そのものが未定なので、固定額で置いている。
	EnemyTradeCost int

	// --- 進行 ---

	// TotalRounds は1ゲームの round 数 (未定 #7)。
	TotalRounds int
	// TokenThreshold は先攻トークンが発動する数。要件で 3 と確定済み。
	TokenThreshold int

	// --- 呪物 ---

	// ValueMin / ValueMax は呪物の価値の範囲(両端を含む)。未定。
	ValueMin, ValueMax int
	// CurselessPercent は「低確率で呪いを持たない呪物」の確率(%)。未定。
	CurselessPercent int

	// --- 呪いの数値 (呪い案 論点 #4。すべて未定) ---

	// RoundTransferPercent は #7 蝕み・#8 実り で、1 round に移す価値の割合(%)。
	RoundTransferPercent int
	// RansomMultiplier は #9 身代金 の支払額の倍率。
	RansomMultiplier int
	// HungerCost は #15 餓え で、調べられるたびに減る所持金。
	HungerCost int
	// ClairvoyanceDetectPercent は #13 千里眼 の、round ごとの発覚確率(%)。
	ClairvoyanceDetectPercent int
}

// DefaultParams は仮の数値一式を返す。プレイテストで差し替える前提。
// 各値の根拠は リポジトリ直下の 仮決定.md にある。
func DefaultParams() Params {
	return Params{
		Rows:          9, // 自陣3 + フリー3 + 自陣3。中央は index 4(仮決定.md §0 案A)
		Cols:          5,
		FieldDepth:    3,
		InitialRelics: 10,

		StartingMoney:  100,
		EnemyTradeCost: 20,

		TotalRounds:    8,
		TokenThreshold: 3,

		ValueMin:         0,
		ValueMax:         50,
		CurselessPercent: 10,

		RoundTransferPercent:      20,
		RansomMultiplier:          2,
		HungerCost:                5,
		ClairvoyanceDetectPercent: 30,
	}
}

func (p Params) validate() error {
	switch {
	case p.Rows < 3 || p.Rows%2 == 0:
		return errors.New("engine: Rows は 3 以上の奇数にする")
	case p.Cols < 1:
		return errors.New("engine: Cols は 1 以上")
	case p.FieldDepth < 1 || p.FieldDepth*2 >= p.Rows:
		return errors.New("engine: FieldDepth が盤に収まらない(フリー地が残らない)")
	case p.TotalRounds < 1:
		return errors.New("engine: TotalRounds は 1 以上")
	case p.TokenThreshold < 1:
		return errors.New("engine: TokenThreshold は 1 以上")
	case p.ValueMin > p.ValueMax:
		return errors.New("engine: ValueMin > ValueMax")
	case p.InitialRelics < 0 || p.InitialRelics > p.freeCells():
		return errors.New("engine: InitialRelics がフリー地のマス数を超える")
	}
	return nil
}

func (p Params) freeCells() int { return (p.Rows - 2*p.FieldDepth) * p.Cols }

// RNG はサーバー側の乱数源。じゃんけん・初期配置・呪いの発覚判定はすべてここから引く。
// シードを固定すれば対戦を再現できる(隠し情報のバグを追うのに必須)。
type RNG interface {
	IntN(n int) int
}

// NewRNG はシードから決定的な乱数源を作る。
func NewRNG(seed uint64) RNG {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// percent は p% の確率で true を返す。
func percent(r RNG, p int) bool {
	if p <= 0 {
		return false
	}
	if p >= 100 {
		return true
	}
	return r.IntN(100) < p
}
