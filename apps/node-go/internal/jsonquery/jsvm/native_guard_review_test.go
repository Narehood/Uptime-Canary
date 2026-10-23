package jsvm

import (
	"context"
	"testing"
)

// Independent fixed graph programs exercise the actual Go validator. They do
// not copy its traversal or install expression-language functions.
func reviewNativeGuard(t *testing.T, script string) {
	t.Helper()
	vm := nativeHelperVM(t, context.Background())
	value, err := vm.RunString(`
function reviewRejects(value) {
    try { canaryFinite(value); return false; }
    catch (error) { return error.message === 'JSON query result rejected by finite profile'; }
}
(function() { ` + script + ` })()`)
	if err != nil {
		t.Fatalf("fixed native guard program failed: %v", err)
	}
	if value == nil || !value.ToBoolean() {
		t.Fatal("native guard assertion failed")
	}
}

func TestNativeGuardReviewDescriptors(t *testing.T) {
	cases := []struct{ name, script string }{
		{"null_prototype_reserved_names", `
const value = Object.create(null);
value.__proto__ = 0; value.constructor = 1; value.hasOwnProperty = 2;
canaryFinite(value);
value.constructor = Infinity;
return reviewRejects(value);`},
		{"own_data_shadows_inherited_getter", `
let reads = 0;
const prototype = {};
Object.defineProperty(prototype, 'x', {get() { reads++; throw 1; }, enumerable: true});
const value = Object.create(prototype);
Object.defineProperty(value, 'x', {value: 1, enumerable: true});
canaryFinite(value);
return reads === 0;`},
		{"inherited_proxy_is_not_traversed", `
let traps = 0;
const prototype = new Proxy({}, {
    get() { traps++; throw 1; }, ownKeys() { traps++; throw 2; },
    getOwnPropertyDescriptor() { traps++; throw 3; }, getPrototypeOf() { traps++; throw 4; }
});
const value = Object.create(prototype);
Object.defineProperty(value, 'x', {value: 1, enumerable: true});
canaryFinite(value);
return traps === 0;`},
		{"sparse_hole_does_not_read_inherited_index", `
let reads = 0;
const prototype = Object.create(Array.prototype);
Object.defineProperty(prototype, '0', {get() { reads++; throw 1; }, enumerable: true});
const value = Array(2);
value[1] = 1;
Object.setPrototypeOf(value, prototype);
canaryFinite(value);
return reads === 0;`},
		{"inherited_index_nonfinite_is_not_own_data", `
const prototype = Object.create(Array.prototype);
prototype[0] = Infinity;
const value = Array(2);
value[1] = 0;
Object.setPrototypeOf(value, prototype);
canaryFinite(value);
return true;`},
		{"nonenumerable_object_data_is_outside_key_domain", `
const value = {x: 1};
Object.defineProperty(value, 'hidden', {value: Infinity});
canaryFinite(value);
return true;`},
		{"nonenumerable_array_index_is_still_a_position", `
const value = [0];
Object.defineProperty(value, '0', {value: Infinity, enumerable: false});
return reviewRejects(value);`},
		{"nonenumerable_array_accessor_is_rejected_unread", `
let reads = 0;
const value = [0];
Object.defineProperty(value, '0', {get() { reads++; throw 1; }, enumerable: false});
return reviewRejects(value) && reads === 0;`},
		{"ordinary_object_length_does_not_select_array_path", `
const value = {0: 1};
Object.defineProperty(value, 'length', {value: 70000});
canaryFinite(value);
return true;`},
		{"own_length_accessor_is_rejected_unread", `
let reads = 0;
const value = {};
Object.defineProperty(value, 'length', {get() { reads++; throw 1; }, enumerable: true});
return reviewRejects(value) && reads === 0;`},
		{"to_string_tag_is_not_classification_authority", `
let reads = 0;
const value = {x: 1};
Object.defineProperty(value, Symbol.toStringTag, {get() { reads++; throw 1; }});
canaryFinite(value);
value.x = Infinity;
return reviewRejects(value) && reads === 0;`},
		{"registered_prototype_does_not_register_child", `
const prototype = {_jsonata_lambda: true, hidden: Infinity};
canaryProcedures.add(prototype);
canaryFinite(prototype);
const value = Object.create(prototype);
value.hidden = Infinity;
return reviewRejects(value);`},
		{"data_value_undefined_remains_a_data_descriptor", `
const value = Object.create(null);
Object.defineProperty(value, 'x', {value: undefined, enumerable: true});
canaryFinite(value);
return true;`},
		// Pinned Goja builds descriptor fields with ordinary assignment. A
		// synthetic getter-only Object.prototype.value prevents construction of
		// its own value field. This environment mutation is not a JSON document;
		// require private rejection, no getter call, and restoration recovery.
		{"descriptor_prototype_poisoning_fails_closed_then_recovers", `
let reads = 0;
const safe = {x: 1}, accessor = {};
Object.defineProperty(accessor, 'x', {get() { reads++; throw 1; }, enumerable: true});
Object.defineProperty(Object.prototype, 'value', {get() { reads++; throw 2; }, configurable: true});
try {
    if (!reviewRejects(safe) || reads !== 0) return false;
    if (!reviewRejects(accessor) || reads !== 0) return false;
} finally { delete Object.prototype.value; }
canaryFinite(safe);
return reviewRejects(accessor) && reads === 0;`},
		{"frozen_data_and_bound_callables", `
let reads = 0;
const callable = (function() { throw 1; }).bind(null);
Object.defineProperty(callable, 'hidden', {get() { reads++; throw 2; }, enumerable: true});
canaryFinite(Object.freeze({value: Object.freeze([1, callable])}));
return reads === 0;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { reviewNativeGuard(t, tc.script) })
	}
}

func TestNativeGuardReviewCombinedLimits(t *testing.T) {
	cases := []struct{ name, script string }{
		{"packed_exact_depth_and_visits", `
let value = Array(65408).fill(0);
for (let i = 0; i < 127; i++) value = {v: value};
canaryFinite(value);
return true;`},
		{"sparse_exact_depth_and_visits", `
let value = Array(65408);
value[65407] = 0;
for (let i = 0; i < 127; i++) value = {v: value};
canaryFinite(value);
return true;`},
		{"packed_excess_visits_at_exact_depth", `
let value = Array(65409).fill(0);
for (let i = 0; i < 127; i++) value = {v: value};
return reviewRejects(value);`},
		{"sparse_excess_visits_at_exact_depth", `
let value = Array(65409);
value[65408] = 0;
for (let i = 0; i < 127; i++) value = {v: value};
return reviewRejects(value);`},
		{"empty_container_at_depth_128", `
let value = [];
for (let i = 0; i < 128; i++) value = {v: value};
canaryFinite(value);
return true;`},
		{"present_undefined_child_at_depth_129", `
let value = [undefined];
for (let i = 0; i < 128; i++) value = {v: value};
return reviewRejects(value);`},
		{"sparse_undefined_child_at_depth_129", `
let value = Array(1);
for (let i = 0; i < 128; i++) value = {v: value};
return reviewRejects(value);`},
		{"repeated_alias_exact_aggregate_then_one_more", `
const shared = Array(32766).fill(0);
const value = {left: shared, right: shared, tail: 0};
canaryFinite(value);
value.extra = 0;
return reviewRejects(value);`},
		{"same_alias_at_shallow_and_exact_depth", `
const shared = {v: 0};
let deep = shared;
for (let i = 0; i < 126; i++) deep = {v: deep};
canaryFinite({shallow: shared, deep: deep});
return true;`},
		{"same_alias_at_shallow_and_excess_depth", `
const shared = {v: 0};
let deep = shared;
for (let i = 0; i < 127; i++) deep = {v: deep};
return reviewRejects({shallow: shared, deep: deep});`},
		{"opaque_identity_at_depth_limit", `
let value = {environment: Infinity};
canaryProcedures.add(value);
for (let i = 0; i < 128; i++) value = {v: value};
canaryFinite(value);
return true;`},
		{"callable_beyond_depth_limit", `
let value = function() {};
for (let i = 0; i < 129; i++) value = {v: value};
return reviewRejects(value);`},
		{"fresh_full_budget_each_boundary", `
const value = Array(65535).fill(0);
canaryFinite(value);
canaryFinite(value);
return true;`},
		{"cycle_rejection_then_repaired_graph", `
const left = {}, right = {left: left};
left.right = right;
if (!reviewRejects([left, right, left])) return false;
delete left.right;
canaryFinite([left, right, left]);
return true;`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { reviewNativeGuard(t, tc.script) })
	}
}

func TestNativeGuardReviewCapturedIntrinsics(t *testing.T) {
	reviewNativeGuard(t, `
let calls = 0;
const opaque = {hidden: Infinity};
canaryProcedures.add(opaque);
const trap = function() { calls++; throw 1; };
Object.getOwnPropertyDescriptor = trap;
Object.hasOwn = trap;
Object.keys = trap;
Array.isArray = trap;
WeakSet.prototype.has = trap;
canaryProcedures.has = trap;
Number.isFinite = trap;
canaryFinite({data: [0, opaque]});
return reviewRejects({data: [Infinity]}) && calls === 0;`)
}
