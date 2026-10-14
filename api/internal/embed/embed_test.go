package embed

import (
	"context"
	"math"
	"testing"
)

func TestLocalEmbeddingIsDeterministic(t *testing.T) {
	p := New(Config{}) // 未配置网关 → 本地回退
	if p.Kind() != "local" {
		t.Fatalf("期望 local 实现，实际 %s", p.Kind())
	}

	vecs, err := p.Embed(context.Background(), []string{"课间追逐打闹可以吗？", "课间追逐打闹可以吗？"})
	if err != nil {
		t.Fatalf("Embed 返回错误: %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("期望 2 条向量，实际 %d", len(vecs))
	}
	if len(vecs[0]) != Dim {
		t.Fatalf("维度应为 %d，实际 %d", Dim, len(vecs[0]))
	}
	for i := range vecs[0] {
		if vecs[0][i] != vecs[1][i] {
			t.Fatalf("同一文本两次嵌入应在第 %d 维一致", i)
		}
	}
}

func TestLocalEmbeddingIsNormalized(t *testing.T) {
	p := New(Config{})
	vecs, err := p.Embed(context.Background(), []string{"校园内不得追逐打闹。"})
	if err != nil {
		t.Fatalf("Embed 返回错误: %v", err)
	}
	if norm := Norm(vecs[0]); math.Abs(norm-1) > 1e-5 {
		t.Errorf("L2 范数应约为 1，实际 %f", norm)
	}
}

func TestLocalEmbeddingSimilarityBehavesLexically(t *testing.T) {
	p := New(Config{})
	vecs, err := p.Embed(context.Background(), []string{
		"校园内不得追逐打闹。",
		"不得在走廊追逐打闹。",
		"光合作用在叶绿体中进行。",
	})
	if err != nil {
		t.Fatalf("Embed 返回错误: %v", err)
	}
	related := dot(vecs[0], vecs[1])
	unrelated := dot(vecs[0], vecs[2])
	if related <= unrelated {
		t.Errorf("字面相近的文本相似度应更高：related=%f unrelated=%f", related, unrelated)
	}
}

func TestGatewaySelectedWhenConfigured(t *testing.T) {
	p := New(Config{BaseURL: "http://gateway.example/v1", Dim: Dim})
	if p.Kind() != "gateway" {
		t.Fatalf("配置了 BaseURL 应走网关，实际 %s", p.Kind())
	}
}

func TestTokenizeProducesCJKBigrams(t *testing.T) {
	toks := tokenize("磁铁AB")
	set := map[string]bool{}
	for _, tk := range toks {
		set[tk] = true
	}
	for _, want := range []string{"磁", "铁", "磁铁", "ab"} {
		if !set[want] {
			t.Errorf("期望 token 含 %q，实际 %v", want, toks)
		}
	}
}

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}
