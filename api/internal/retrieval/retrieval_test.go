package retrieval

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestRRFSinglePathScore(t *testing.T) {
	hits := []rawHit{{ChunkID: 11}, {ChunkID: 12}, {ChunkID: 13}}

	got := rankSingle(hits, ModeKeyword, 60)
	if len(got) != 3 {
		t.Fatalf("期望 3 条，实际 %d", len(got))
	}
	for i, h := range got {
		want := 1 / float64(60+i+1)
		if math.Abs(h.ScoreFinal-want) > 1e-12 {
			t.Errorf("第 %d 条合成分应为 %f，实际 %f", i, want, h.ScoreFinal)
		}
		if h.RankKeyword == nil || *h.RankKeyword != i+1 {
			t.Errorf("第 %d 条关键字名次错误: %v", i, h.RankKeyword)
		}
		if h.RankVector != nil {
			t.Errorf("关键字模式不应有向量名次")
		}
		if h.ScoreVector != nil {
			t.Errorf("关键字模式不应有向量分数")
		}
	}
}

func TestRRFFuseSumsBothPaths(t *testing.T) {
	kw := []rawHit{{ChunkID: 1, ScoreKeyword: 8.3}, {ChunkID: 2, ScoreKeyword: 2.7}}
	vec := []rawHit{{ChunkID: 1, ScoreVector: 0.43}, {ChunkID: 3, ScoreVector: 0.41}}

	got := fuse(kw, vec, 60)

	byID := map[int64]Hit{}
	for _, h := range got {
		byID[h.ChunkID] = h
	}

	// 两路都命中的第 1 条：1/61 + 1/61
	if want := 2 / 61.0; math.Abs(byID[1].ScoreFinal-want) > 1e-12 {
		t.Errorf("两路都命中的合成分应为 %f，实际 %f", want, byID[1].ScoreFinal)
	}
	// 仅关键字命中的第 2 条：1/62
	if want := 1 / 62.0; math.Abs(byID[2].ScoreFinal-want) > 1e-12 {
		t.Errorf("仅关键字命中的合成分应为 %f，实际 %f", want, byID[2].ScoreFinal)
	}
	// 仅向量命中的第 3 条：1/62
	if want := 1 / 62.0; math.Abs(byID[3].ScoreFinal-want) > 1e-12 {
		t.Errorf("仅向量命中的合成分应为 %f，实际 %f", want, byID[3].ScoreFinal)
	}

	if byID[2].RankVector != nil || byID[2].ScoreVector != nil {
		t.Errorf("缺席的向量一路不应贡献分数")
	}
}

func TestRRFFuseRanksBothPathsFirst(t *testing.T) {
	kw := []rawHit{{ChunkID: 1}, {ChunkID: 2}}
	vec := []rawHit{{ChunkID: 2}, {ChunkID: 1}}

	got := fuse(kw, vec, 60)
	if len(got) != 2 {
		t.Fatalf("期望 2 条，实际 %d", len(got))
	}
	// 两路名次之和相同，按切片主键升序保证稳定。
	if got[0].ChunkID != 1 || got[1].ChunkID != 2 {
		t.Errorf("同分时应按主键升序，实际 %d, %d", got[0].ChunkID, got[1].ChunkID)
	}
}

func TestRRFFuseBothHitOutranksSingleHit(t *testing.T) {
	kw := []rawHit{{ChunkID: 1}, {ChunkID: 9}}
	vec := []rawHit{{ChunkID: 9}, {ChunkID: 1}}

	got := fuse(kw, vec, 60)
	// 两条都被两路命中：名次为 (1,2) 与 (2,1)，合成分相同。
	want := 1/61.0 + 1/62.0
	for _, h := range got {
		if math.Abs(h.ScoreFinal-want) > 1e-12 {
			t.Errorf("两路都命中时合成分应为 %f，实际 %f", want, h.ScoreFinal)
		}
	}

	// 只命中一路时，合成分必然低于两路都命中。
	single := fuse([]rawHit{{ChunkID: 1}}, nil, 60)
	if single[0].ScoreFinal >= got[0].ScoreFinal {
		t.Errorf("仅命中一路的分数应低于两路都命中：%f vs %f", single[0].ScoreFinal, got[0].ScoreFinal)
	}
}

func TestSearchRejectsShortQuery(t *testing.T) {
	s := New(nil, nil, nil, 0.35, 60, 10)

	for _, q := range []string{"", " ", "啊"} {
		if _, err := s.Search(context.Background(), 1, q, ModeHybrid); !errors.Is(err, ErrQueryTooShort) {
			t.Errorf("问句 %q 应被拒绝，实际 err=%v", q, err)
		}
	}
}

func TestSearchRejectsUnknownMode(t *testing.T) {
	s := New(nil, nil, nil, 0.35, 60, 10)
	if _, err := s.Search(context.Background(), 1, "追逐打闹", "semantic"); !errors.Is(err, ErrBadMode) {
		t.Errorf("未知模式应被拒绝，实际 err=%v", err)
	}
}

func TestDefaultsAreApplied(t *testing.T) {
	s := New(nil, nil, nil, 0, 0, 0)
	if s.Threshold != 0.35 {
		t.Errorf("默认阈值应为 0.35，实际 %f", s.Threshold)
	}
	if s.RRFK != 60 {
		t.Errorf("默认 RRF k 应为 60，实际 %d", s.RRFK)
	}
	if s.Limit != 10 {
		t.Errorf("默认条数应为 10，实际 %d", s.Limit)
	}
}

func TestTopTruncates(t *testing.T) {
	hits := []Hit{{ChunkID: 1}, {ChunkID: 2}, {ChunkID: 3}}
	if got := Top(hits, 4); len(got) != 3 {
		t.Errorf("n 大于长度时应返回全部，实际 %d", len(got))
	}
	if got := Top(hits, 2); len(got) != 2 || got[1].ChunkID != 2 {
		t.Errorf("Top(2) 结果不正确: %+v", got)
	}
}
