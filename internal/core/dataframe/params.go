package dataframe

// Render-time parameter binding for a Transform pipeline. A UI Transform binding
// carries named params whose values are resolved at render time (from a
// Selection.defaultValue, a Filter, host state, …). The reference evaluator is
// deliberately strict — a Param / InParam expression that reaches evalExpr is an
// UnboundParam error (see evaluate.go) — so params are resolved by SUBSTITUTION
// before evaluation, exactly as the evaluator's own doc comment prescribes. This
// is pure pipeline rewriting; the evaluator's semantics are untouched.

// BindParams rewrites a pipeline for evaluation: every reference to a bound param
// (name present in env) becomes a Lit of its resolved cell, and every FILTER step
// referencing an UNBOUND param (name in unbound) is pruned — the one lenient
// "unset choice filter ⇒ no constraint" rule. A non-filter step (e.g. derive)
// that references an unbound param is left intact, so the untouched evaluator
// still surfaces UnboundParam loudly rather than a silently-wrong result.
//
// A caller that also has LIST params runs [SubstituteListParams] FIRST and passes
// its result here. That ORDER is load-bearing, not a convention: a substituted
// InParam has become an InList and names no param at all, so it survives this
// prune, while an unbound one still names its own and is pruned by exactly the
// rule above — which is why one prune covers both param kinds with no second
// rule for lists.
func BindParams(pipeline []Transform, env map[string]Cell, unbound map[string]bool) []Transform {
	out := make([]Transform, 0, len(pipeline))
	for _, step := range pipeline {
		if f, ok := step.(Filter); ok && referencesUnbound(exprParamNames(f.Pred), unbound) {
			continue // prune the filter — its choice param is unset
		}
		out = append(out, bindStep(step, env))
	}
	return out
}

func referencesUnbound(names []string, unbound map[string]bool) bool {
	for _, n := range names {
		if unbound[n] {
			return true
		}
	}
	return false
}

// bindStep substitutes bound params inside the only two param-bearing steps
// (Filter.Pred, Derive.Expr); every other step has no expression to rewrite.
func bindStep(step Transform, env map[string]Cell) Transform {
	switch v := step.(type) {
	case Filter:
		return Filter{Pred: substExpr(v.Pred, env)}
	case Derive:
		return Derive{Name: v.Name, Expr: substExpr(v.Expr, env)}
	default:
		return step
	}
}

// exprParamNames collects every Param / InParam name an expression references.
func exprParamNames(e ColExpr) []string {
	var out []string
	var walk func(ColExpr)
	walk = func(e ColExpr) {
		switch x := e.(type) {
		case Param:
			out = append(out, x.Name)
		case InParam:
			walk(x.Subject)
			out = append(out, x.Name)
		case Binary:
			walk(x.Left)
			walk(x.Right)
		case Not:
			walk(x.Expr)
		case Coalesce:
			for _, s := range x.Exprs {
				walk(s)
			}
		case Case:
			for _, wt := range x.Cases {
				walk(wt.When)
				walk(wt.Then)
			}
			walk(x.ElseExpr)
		case Cast:
			walk(x.Expr)
		case ApplyFn:
			for _, a := range x.Args {
				walk(a)
			}
		case InList:
			walk(x.Subject)
			for _, it := range x.Items {
				walk(it)
			}
		case IsNull:
			walk(x.Expr)
		case Col, Lit:
			// leaf, no params
		}
	}
	walk(e)
	return out
}

// SubstituteListParams resolves LIST-valued params by SUBSTITUTION: every
// InParam whose name listEnv binds becomes an InList over that selection's cells
// as literals, so the membership test reaching the evaluator names no param at
// all. An InParam listEnv does not bind is left intact — which is what lets the
// caller's prune see it under its own name, and what makes an unsubstituted one
// reaching the evaluator a strict UnboundParam rather than a silent pass.
//
// The list-valued twin of the scalar substitution [BindParams] performs, and
// deliberately disjoint from it: the scalar env never binds an InParam and this
// list env never binds a scalar Param, so a KIND MISMATCH in either direction
// substitutes nothing and reaches that same strict refusal, never a silently
// wrong scoping.
//
// The EMPTY-selection rule is the CALLER'S, not this function's, and that
// division is deliberate: "nothing selected" is the absence of a constraint, not
// a constraint no row satisfies, so a host records an empty selection as UNBOUND
// (whereupon the filter step is pruned and the frame is unconstrained) rather
// than passing an empty cell slice here, which would substitute an InList that
// matches nothing. Binding a name to an empty slice is therefore a caller error,
// not a spelling of deselection.
func SubstituteListParams(pipeline []Transform, listEnv map[string][]Cell) []Transform {
	if len(listEnv) == 0 {
		return pipeline
	}
	out := make([]Transform, len(pipeline))
	for i, step := range pipeline {
		switch v := step.(type) {
		case Filter:
			out[i] = Filter{Pred: substListExpr(v.Pred, listEnv)}
		case Derive:
			out[i] = Derive{Name: v.Name, Expr: substListExpr(v.Expr, listEnv)}
		default:
			out[i] = step
		}
	}
	return out
}

// substListExpr is SubstituteListParams' expression walk — the same traversal
// substExpr performs, binding the other param kind.
func substListExpr(e ColExpr, listEnv map[string][]Cell) ColExpr {
	switch x := e.(type) {
	case InParam:
		subject := substListExpr(x.Subject, listEnv)
		cells, ok := listEnv[x.Name]
		if !ok {
			return InParam{Subject: subject, Name: x.Name}
		}
		items := make([]ColExpr, len(cells))
		for i, c := range cells {
			items[i] = Lit{Cell: c}
		}
		return InList{Subject: subject, Items: items}
	case Binary:
		return Binary{Op: x.Op, Left: substListExpr(x.Left, listEnv), Right: substListExpr(x.Right, listEnv)}
	case Not:
		return Not{Expr: substListExpr(x.Expr, listEnv)}
	case Coalesce:
		xs := make([]ColExpr, len(x.Exprs))
		for i, s := range x.Exprs {
			xs[i] = substListExpr(s, listEnv)
		}
		return Coalesce{Exprs: xs}
	case Case:
		cs := make([]WhenThen, len(x.Cases))
		for i, wt := range x.Cases {
			cs[i] = WhenThen{When: substListExpr(wt.When, listEnv), Then: substListExpr(wt.Then, listEnv)}
		}
		return Case{Cases: cs, ElseExpr: substListExpr(x.ElseExpr, listEnv)}
	case Cast:
		return Cast{Type: x.Type, Expr: substListExpr(x.Expr, listEnv)}
	case ApplyFn:
		args := make([]ColExpr, len(x.Args))
		for i, a := range x.Args {
			args[i] = substListExpr(a, listEnv)
		}
		return ApplyFn{Fn: x.Fn, Args: args}
	case InList:
		items := make([]ColExpr, len(x.Items))
		for i, it := range x.Items {
			items[i] = substListExpr(it, listEnv)
		}
		return InList{Subject: substListExpr(x.Subject, listEnv), Items: items}
	case IsNull:
		return IsNull{Expr: substListExpr(x.Expr, listEnv)}
	default: // Col, Lit, Param — a scalar param is never bound here
		return e
	}
}

// substExpr replaces every bound Param with a Lit of its resolved cell; an
// unbound Param (absent from env) is left intact so the evaluator surfaces
// UnboundParam. InParam is a list param — the scalar env never binds it, so it
// passes through unchanged (see [SubstituteListParams]).
func substExpr(e ColExpr, env map[string]Cell) ColExpr {
	switch x := e.(type) {
	case Param:
		if cell, ok := env[x.Name]; ok {
			return Lit{Cell: cell}
		}
		return x
	case Binary:
		return Binary{Op: x.Op, Left: substExpr(x.Left, env), Right: substExpr(x.Right, env)}
	case Not:
		return Not{Expr: substExpr(x.Expr, env)}
	case Coalesce:
		xs := make([]ColExpr, len(x.Exprs))
		for i, s := range x.Exprs {
			xs[i] = substExpr(s, env)
		}
		return Coalesce{Exprs: xs}
	case Case:
		cs := make([]WhenThen, len(x.Cases))
		for i, wt := range x.Cases {
			cs[i] = WhenThen{When: substExpr(wt.When, env), Then: substExpr(wt.Then, env)}
		}
		return Case{Cases: cs, ElseExpr: substExpr(x.ElseExpr, env)}
	case Cast:
		return Cast{Type: x.Type, Expr: substExpr(x.Expr, env)}
	case ApplyFn:
		args := make([]ColExpr, len(x.Args))
		for i, a := range x.Args {
			args[i] = substExpr(a, env)
		}
		return ApplyFn{Fn: x.Fn, Args: args}
	case InList:
		items := make([]ColExpr, len(x.Items))
		for i, it := range x.Items {
			items[i] = substExpr(it, env)
		}
		return InList{Subject: substExpr(x.Subject, env), Items: items}
	case InParam:
		return InParam{Subject: substExpr(x.Subject, env), Name: x.Name}
	case IsNull:
		return IsNull{Expr: substExpr(x.Expr, env)}
	default: // Col, Lit
		return e
	}
}
