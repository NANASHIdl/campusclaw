package skill

import "strings"

// Route 在候选集里选出一个技能：按适用条件切词，命中一个计 1 分，取最高分。
//
// 候选集须已按 id 升序（ListEnabledBound 保证），因此「严格大于才替换」
// 天然实现「同分取编号较小者」。零分返回 ok=false。
func Route(question string, candidates []Skill) (Skill, bool) {
	lowerQ := strings.ToLower(question)
	best := Skill{}
	bestScore := 0
	for _, s := range candidates {
		score := 0
		for _, w := range SplitTriggers(s.Trigger) {
			if w != "" && strings.Contains(lowerQ, strings.ToLower(w)) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = s, score
		}
	}
	return best, bestScore > 0
}
