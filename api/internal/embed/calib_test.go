package embed

import (
	"context"
	"testing"

	"campusclaw/api/internal/chunk"
)

const calibDoc = `# 校园安全与行为规范

## 课间活动

上课铃响后必须进入教室，不得在走廊停留。校园内不得追逐打闹。
课间可以喝水、去卫生间、做眼保健操的准备。禁止在楼道奔跑，禁止推搡和拍打他人。
球类只在体育课和指定场地进行。下雨时在走廊行走要慢，以免滑倒。
值日生在下课铃后擦黑板、摆齐桌椅，离开时关灯关窗。

## 实验与体育课

实验课按教师演示操作，不随意混合试剂。体育课做准备活动，不在器械上打闹。
体育课感到胸痛或眩晕，立即停止并报告教师，不硬撑着上课。
使用锋利工具时双手握稳，刃口朝外传递，不得朝向他人。

## 消防与应急

消防演习时按路线撤离，不乘坐电梯，不返回教室取物品。
发现烟雾或异味立即告知最近的教师，并按疏散指示离开教学楼。

## 图书馆与自习

进入阅览室保持安静，手机调成静音。一次借阅不超过规定册数，到期可在柜台续借一次。
晚自习保持安静，需要讨论时到教师指定的角落并压低声音。不在自习时间串座。
`

// TestCalibrationSimilarityScale 校准本地回退嵌入的相似度尺度：
// 相关问句应高于阈值 0.35，不相关问句应低于阈值。
func TestCalibrationSimilarityScale(t *testing.T) {
	chunks, err := chunk.Split(calibDoc, chunk.Options{Strategy: chunk.StrategyHierarchy})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	p := New(Config{})
	texts := []string{"课间追逐打闹可以吗？", "明天的天气怎么样？", "光合作用的暗反应在哪里进行？"}
	for _, c := range chunks {
		texts = append(texts, c.Text)
	}
	vecs, err := p.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	names := []string{"相关：课间追逐打闹", "不相关：天气", "不相关：光合作用"}
	for qi := 0; qi < 3; qi++ {
		t.Logf("--- 问句 %s ---", names[qi])
		for ci, c := range chunks {
			sim := dot(vecs[qi], vecs[3+ci])
			t.Logf("  余弦 %.4f  <- 第 %d 片: %.24s", sim, c.Index, c.Text)
		}
	}

	// 明确相关的问句应高于阈值。
	best := 0.0
	for ci := range chunks {
		if s := dot(vecs[0], vecs[3+ci]); s > best {
			best = s
		}
	}
	if best < 0.35 {
		t.Errorf("相关问句最高余弦 %.4f 低于阈值 0.35，背景分量偏小", best)
	}

	// 与材料完全无关的问句（天气）应低于阈值。
	worstUnrelated := 0.0
	for ci := range chunks {
		if s := dot(vecs[1], vecs[3+ci]); s > worstUnrelated {
			worstUnrelated = s
		}
	}
	if worstUnrelated >= 0.35 {
		t.Errorf("不相关问句最高余弦 %.4f 不低于阈值 0.35，背景分量偏大", worstUnrelated)
	}
	t.Logf("校准结果：相关最高 %.4f，不相关最高 %.4f", best, worstUnrelated)
}
