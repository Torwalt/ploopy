// Package ploopy carries the documents the binary ships with, so an installed
// ploopy works in a repository that has none of its own.
package ploopy

import "embed"

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

//go:embed skills/review-direct/SKILL.md
var ReviewDirectSkill string

// UnitRescueSkill is how a rescue session diagnoses a unit the loop stopped
// on. A repository may override it at .agents/skills/unit-rescue/SKILL.md.
//
//go:embed skills/unit-rescue/SKILL.md
var UnitRescueSkill string

// LandRepairSkill is how a session repairs a conflict or a failing check while
// a branch is landed. A repository may override it at
// .agents/skills/land-repair/SKILL.md.
//
//go:embed skills/land-repair/SKILL.md
var LandRepairSkill string

// Skills is every skill the binary ships, a directory each, for `ploopy
// skills` to write into a repository.
//
//go:embed skills
var Skills embed.FS
