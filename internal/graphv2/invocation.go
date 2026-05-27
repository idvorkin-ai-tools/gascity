// Package graphv2 centralizes graph.v2 input-convoy invocation rules.
package graphv2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/gastownhall/gascity/internal/beads"
	convoycore "github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/molecule"
)

var keyedLocks [256]sync.Mutex

const (
	// ConvoyIDVar is the reserved system variable passed to targeted graph.v2
	// formula invocations.
	ConvoyIDVar = "convoy_id"

	// RuntimeVarsMetadataKey stores the caller/runtime vars a graph.v2 workflow
	// root received, excluding graph.v2 reserved variables injected by runtime.
	RuntimeVarsMetadataKey = "gc.graphv2_vars.v1"

	syntheticMetadataKey     = "gc.synthetic"
	previewInputConvoyPrefix = "preview-input-convoy:"
)

var graphV2ReservedVarNames = map[string]struct{}{
	ConvoyIDVar: {},
	"issue":     {},
	"bead_id":   {},
}

// Invocation describes a normalized graph.v2 formula invocation.
type Invocation struct {
	Formula     *formula.Formula
	FormulaName string
	InputConvoy string
	Vars        map[string]string
	Targeted    bool
}

// LoadFormula resolves a formula without compiling it to a recipe.
func LoadFormula(formulaName string, searchPaths []string) (*formula.Formula, error) {
	parser := formula.NewParser(searchPaths...)
	loaded, err := parser.LoadByName(formulaName)
	if err != nil {
		return nil, err
	}
	return parser.Resolve(loaded)
}

// IsGraphV2Formula reports whether the named formula declares graph.v2.
func IsGraphV2Formula(formulaName string, searchPaths []string) (bool, *formula.Formula, error) {
	resolved, err := LoadFormula(formulaName, searchPaths)
	if err != nil {
		return false, nil, err
	}
	return strings.EqualFold(strings.TrimSpace(resolved.Contract), "graph.v2"), resolved, nil
}

// PrepareInvocation validates and normalizes a graph.v2 invocation. Non-graph
// formulas are returned with Formula set and no input convoy.
func PrepareInvocation(ctx context.Context, store beads.Store, formulaName string, searchPaths []string, targetID string, vars map[string]string) (Invocation, error) {
	resolved, err := LoadFormula(formulaName, searchPaths)
	if err != nil {
		return Invocation{}, err
	}

	inv := Invocation{
		Formula:     resolved,
		FormulaName: formulaName,
		Vars:        maps.Clone(vars),
		Targeted:    strings.TrimSpace(targetID) != "",
	}
	if inv.Vars == nil {
		inv.Vars = make(map[string]string)
	}
	if !strings.EqualFold(strings.TrimSpace(resolved.Contract), "graph.v2") {
		return inv, nil
	}
	if err := validateReservedFormulaVars(resolved); err != nil {
		return Invocation{}, err
	}
	if err := ValidateNoReservedUserVars(inv.Vars); err != nil {
		return Invocation{}, err
	}

	inv.Vars = EffectiveRuntimeVars(resolved, inv.Vars)
	recipe, err := formula.CompileWithoutRuntimeVarValidation(ctx, formulaName, searchPaths, inv.Vars)
	if err != nil {
		return Invocation{}, err
	}
	if err := molecule.ValidateRecipeRuntimeVars(recipe, molecule.Options{Vars: varsWithConvoyPlaceholder(inv.Vars)}); err != nil {
		return Invocation{}, err
	}

	if !inv.Targeted {
		if recipeReferencesReservedInput(recipe) {
			return Invocation{}, fmt.Errorf("convoy_id requires a targeted graph.v2 invocation")
		}
		return inv, nil
	}
	if store == nil {
		return Invocation{}, fmt.Errorf("graph.v2 formula %q requires a bead store to normalize target %s", formulaName, targetID)
	}
	convoyID, err := NormalizeInputConvoy(store, targetID)
	if err != nil {
		return Invocation{}, err
	}
	inv.InputConvoy = convoyID
	inv.Vars[ConvoyIDVar] = convoyID
	if err := molecule.ValidateRecipeRuntimeVars(recipe, molecule.Options{Vars: inv.Vars}); err != nil {
		return Invocation{}, err
	}
	return inv, nil
}

func varsWithConvoyPlaceholder(vars map[string]string) map[string]string {
	out := maps.Clone(vars)
	if out == nil {
		out = make(map[string]string, 1)
	}
	out[ConvoyIDVar] = "graphv2-validation-placeholder"
	return out
}

// EffectiveRuntimeVars returns formula defaults overlaid by caller vars.
func EffectiveRuntimeVars(f *formula.Formula, vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars))
	if f != nil {
		for name, def := range f.Vars {
			if def == nil || def.Default == nil {
				continue
			}
			out[name] = *def.Default
		}
	}
	for key, value := range vars {
		out[key] = value
	}
	if len(out) == 0 {
		return map[string]string{}
	}
	return out
}

// ValidateNoReservedUserVars rejects caller-supplied values for graph.v2
// reserved variables before the runtime injects convoy_id.
func ValidateNoReservedUserVars(vars map[string]string) error {
	for key := range vars {
		switch strings.TrimSpace(key) {
		case ConvoyIDVar, "issue", "bead_id":
			return fmt.Errorf("graph.v2 reserved variable %q cannot be supplied by the caller", key)
		}
	}
	return nil
}

func validateReservedFormulaVars(f *formula.Formula) error {
	if f == nil {
		return nil
	}
	var errs []string
	for name := range f.Vars {
		if _, reserved := graphV2ReservedVarNames[strings.TrimSpace(name)]; reserved {
			errs = append(errs, fmt.Sprintf("vars.%s: graph.v2 reserved variable cannot be declared", name))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("graph.v2 reserved variable validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

func validateGraphV2RecipeReservedSymbols(recipe *formula.Recipe, allowConvoyReference bool) error {
	if recipe == nil {
		return nil
	}
	var errs []string
	for _, step := range recipe.Steps {
		if step.Title != "" {
			collectReservedRefErrors("title", step.Title, allowConvoyReference, &errs)
		}
		if step.Description != "" {
			collectReservedRefErrors("description", step.Description, allowConvoyReference, &errs)
		}
		if step.Notes != "" {
			collectReservedRefErrors("notes", step.Notes, allowConvoyReference, &errs)
		}
		if step.Assignee != "" {
			collectReservedRefErrors("assignee", step.Assignee, allowConvoyReference, &errs)
		}
		for key, value := range step.Metadata {
			if value != "" {
				collectReservedRefErrors("metadata."+key, value, allowConvoyReference, &errs)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("graph.v2 reserved variable validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

func recipeReferencesReservedInput(recipe *formula.Recipe) bool {
	if recipe == nil {
		return false
	}
	for _, step := range recipe.Steps {
		if containsReservedReference(step.Title, ConvoyIDVar) || containsReservedReference(step.Description, ConvoyIDVar) || containsReservedReference(step.Notes, ConvoyIDVar) || containsReservedReference(step.Assignee, ConvoyIDVar) {
			return true
		}
		for _, value := range step.Metadata {
			if containsReservedReference(value, ConvoyIDVar) {
				return true
			}
		}
	}
	return false
}

func collectReservedRefErrors(path, value string, allowConvoyReference bool, errs *[]string) {
	for _, name := range []string{"issue", "bead_id", ConvoyIDVar} {
		if !containsReservedReference(value, name) {
			continue
		}
		if name == ConvoyIDVar {
			if allowConvoyReference {
				continue
			}
			*errs = append(*errs, fmt.Sprintf("%s: convoy_id requires a targeted graph.v2 invocation", path))
			continue
		}
		*errs = append(*errs, fmt.Sprintf("%s: %s is not available in graph.v2 formulas; use convoy_id", path, name))
	}
}

func containsReservedReference(value, name string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	needle := name
	if !strings.Contains(value, needle) {
		return false
	}
	return strings.Contains(value, "{{")
}

// NormalizeInputConvoy returns targetID when it is already a convoy, otherwise
// it creates a visible system-created one-item convoy tracking targetID.
func NormalizeInputConvoy(store beads.Store, targetID string) (string, error) {
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return "", fmt.Errorf("graph.v2 target is required")
	}
	target, err := store.Get(targetID)
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return "", fmt.Errorf("graph.v2 target %s not found: %w", targetID, err)
		}
		return "", fmt.Errorf("loading graph.v2 target %s: %w", targetID, err)
	}
	if convoycore.IsTerminalStatus(target.Status) {
		return "", fmt.Errorf("graph.v2 target %s is %s", target.ID, target.Status)
	}
	if target.Type == "convoy" {
		return target.ID, nil
	}
	inputConvoy, err := CreateSingleItemInputConvoy(store, target)
	if err != nil {
		return "", err
	}
	return inputConvoy.ID, nil
}

// CreateSingleItemInputConvoy creates a system-created one-item convoy for a
// graph.v2 invocation target.
func CreateSingleItemInputConvoy(store beads.Store, target beads.Bead) (beads.Bead, error) {
	if convoycore.IsTerminalStatus(target.Status) {
		return beads.Bead{}, fmt.Errorf("graph.v2 target %s is %s", target.ID, target.Status)
	}
	if strings.TrimSpace(target.ID) == "" {
		return beads.Bead{}, fmt.Errorf("input convoy target id is empty")
	}
	metadata := map[string]string{
		syntheticMetadataKey: "true",
	}
	created, err := store.Create(beads.Bead{
		Title:    "input convoy for " + target.ID,
		Type:     "convoy",
		Priority: target.Priority,
		Metadata: metadata,
	})
	if err != nil {
		return beads.Bead{}, fmt.Errorf("creating input convoy for %s: %w", target.ID, err)
	}
	if err := convoycore.TrackItem(store, created.ID, target.ID); err != nil {
		return beads.Bead{}, fmt.Errorf("tracking %s from input convoy %s: %w", target.ID, created.ID, err)
	}
	return created, nil
}

// PreparePreviewInvocation validates graph.v2 preview inputs without creating
// input convoys or workflow roots.
func PreparePreviewInvocation(ctx context.Context, store beads.Store, formulaName string, searchPaths []string, targetID string, userVars map[string]string) (Invocation, error) {
	resolved, err := LoadFormula(formulaName, searchPaths)
	if err != nil {
		return Invocation{}, fmt.Errorf("loading formula %q: %w", formulaName, err)
	}
	inv := Invocation{
		Formula:     resolved,
		FormulaName: formulaName,
		Vars:        maps.Clone(userVars),
		Targeted:    strings.TrimSpace(targetID) != "",
	}
	if inv.Vars == nil {
		inv.Vars = make(map[string]string)
	}
	if !strings.EqualFold(strings.TrimSpace(resolved.Contract), "graph.v2") {
		return inv, nil
	}
	if err := validateReservedFormulaVars(resolved); err != nil {
		return Invocation{}, err
	}
	if err := ValidateNoReservedUserVars(inv.Vars); err != nil {
		return Invocation{}, err
	}

	inv.Vars = EffectiveRuntimeVars(resolved, inv.Vars)
	recipe, err := formula.CompileWithoutRuntimeVarValidation(ctx, formulaName, searchPaths, inv.Vars)
	if err != nil {
		return Invocation{}, err
	}
	if err := molecule.ValidateRecipeRuntimeVars(recipe, molecule.Options{Vars: varsWithConvoyPlaceholder(inv.Vars)}); err != nil {
		return Invocation{}, err
	}
	if !inv.Targeted {
		if recipeReferencesReservedInput(recipe) {
			return Invocation{}, fmt.Errorf("convoy_id requires a targeted graph.v2 invocation")
		}
		return inv, nil
	}
	if err := validateGraphV2RecipeReservedSymbols(recipe, true); err != nil {
		return Invocation{}, err
	}
	inputConvoyID, err := PreviewInputConvoyID(store, targetID)
	if err != nil {
		return Invocation{}, err
	}
	inv.InputConvoy = inputConvoyID
	inv.Vars[ConvoyIDVar] = inputConvoyID
	return inv, nil
}

// PreviewInputConvoyID returns the read-only input convoy ID a graph.v2 preview
// should use for targetID without creating an input convoy.
func PreviewInputConvoyID(store beads.Store, targetID string) (string, error) {
	targetID = strings.TrimSpace(targetID)
	if store == nil {
		return "", fmt.Errorf("graph.v2 preview requires a bead store")
	}
	target, err := store.Get(targetID)
	if err != nil {
		if errors.Is(err, beads.ErrNotFound) {
			return "", fmt.Errorf("graph.v2 target %s not found: %w", targetID, err)
		}
		return "", fmt.Errorf("loading graph.v2 target %s: %w", targetID, err)
	}
	if convoycore.IsTerminalStatus(target.Status) {
		return "", fmt.Errorf("graph.v2 target %s is %s", target.ID, target.Status)
	}
	if target.Type == "convoy" {
		return target.ID, nil
	}
	return previewInputConvoyPrefix + target.ID, nil
}

// LockKey serializes process-local graph.v2 materialization for a deterministic
// key and returns an unlock function.
func LockKey(key string) func() {
	return lockKey(key)
}

func lockKey(key string) func() {
	mu := &keyedLocks[lockStripe(key)]
	mu.Lock()
	return mu.Unlock
}

func lockStripe(key string) uint8 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return uint8(h.Sum32())
}

// RootKey returns the stable graph.v2 workflow root key for an input convoy and
// invocation variables.
func RootKey(inputConvoyID, formulaName string, vars map[string]string, scopeKind, scopeRef string) string {
	return "graphv2-root:" + strings.TrimSpace(inputConvoyID) + ":" + strings.TrimSpace(formulaName) + ":" + varsFingerprint(vars) + ":" + dispatchScope(scopeKind, scopeRef)
}

func varsFingerprint(vars map[string]string) string {
	if len(vars) == 0 {
		return "empty"
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		if strings.TrimSpace(key) == ConvoyIDVar {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "empty"
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		h.Write([]byte(key))
		h.Write([]byte{0})
		h.Write([]byte(vars[key]))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func dispatchScope(scopeKind, scopeRef string) string {
	scopeKind = strings.TrimSpace(scopeKind)
	scopeRef = strings.TrimSpace(scopeRef)
	if scopeKind == "" && scopeRef == "" {
		return "default"
	}
	return scopeKind + "=" + scopeRef
}

// RuntimeVarsMetadata encodes non-reserved runtime vars for persistence on a
// graph.v2 workflow root. It returns an empty string when no vars need storage.
func RuntimeVarsMetadata(vars map[string]string) string {
	filtered := nonReservedRuntimeVars(vars)
	if len(filtered) == 0 {
		return ""
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return ""
	}
	return string(data)
}

// ParseRuntimeVarsMetadata decodes RuntimeVarsMetadata output, dropping any
// graph.v2 reserved vars defensively.
func ParseRuntimeVarsMetadata(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	return nonReservedRuntimeVars(decoded), nil
}

func nonReservedRuntimeVars(vars map[string]string) map[string]string {
	if len(vars) == 0 {
		return nil
	}
	out := make(map[string]string, len(vars))
	for key, value := range vars {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		switch trimmed {
		case ConvoyIDVar, "issue", "bead_id":
			continue
		default:
			out[trimmed] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
