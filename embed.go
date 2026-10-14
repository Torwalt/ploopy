// Package ploopy carries the documents the binary ships with, so an installed
// ploopy works in a repository that has none of its own.
package ploopy

import _ "embed"

// PlanUnitSkill is how a session carries out one unit. A repository may
// override it; see config.SkillPath.
//
//go:embed skills/plan-unit/SKILL.md
var PlanUnitSkill string

// PlanWritingSkill is how a plan the loop can run is written.
//
//go:embed skills/plan-writing/SKILL.md
var PlanWritingSkill string

// PlanReviewSkill is how a review session collects findings on landed work.
// A repository may override it at .agents/skills/plan-review/SKILL.md.
//
//go:embed skills/plan-review/SKILL.md
var PlanReviewSkill string

// ReviewTriageSkill is how the triage weighs the findings and writes the fix
// plan. A repository may override it at .agents/skills/review-triage/SKILL.md.
//
//go:embed skills/review-triage/SKILL.md
var ReviewTriageSkill string
