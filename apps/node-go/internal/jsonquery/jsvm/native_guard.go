package jsvm

import (
	"context"
	"math"
	"reflect"
	"strconv"
	"sync/atomic"

	"github.com/dop251/goja"
)

const privateFiniteError = "JSON query result rejected by finite profile"

// This validator is passed only as a trusted bootstrap argument. It is never
// installed in the global object or JSONata's environment. No Go data object,
// expression function, export bridge, or evaluator fallback is provided.
//
// The only object type metadata query is the explicitly authorized ExportType
// comparison against Proxy, before callable/identity/descriptor/key operations.
// No traversed object is exported, reflected over, coerced, or read via Get().
func privateValidator(vm *goja.Runtime, ctx context.Context, interrupted *atomic.Bool) func(goja.FunctionCall) goja.Value {
	// Builtin objects/functions are trusted and captured before user evaluation.
	object := vm.Get("Object").(*goja.Object)
	descriptor, _ := goja.AssertFunction(object.Get("getOwnPropertyDescriptor"))
	own, _ := goja.AssertFunction(object.Get("hasOwn"))
	weak := vm.Get("WeakSet").(*goja.Object)
	has, _ := goja.AssertFunction(weak.Get("prototype").(*goja.Object).Get("has"))
	proxyType := reflect.TypeOf(goja.Proxy{})
	valueKey := vm.ToValue("value")
	prototype := object.Get("prototype").(*goja.Object)
	fast := false
	active := false
	reject := func() { panic(vm.NewTypeError(privateFiniteError)) }
	cancelled := func() bool { return ctx.Err() != nil || interrupted.Load() }

	// A descriptor is a fresh ordinary object produced by the captured native
	// builtin. Only after its own data-value field is verified is that trusted
	// descriptor read. Get is never used on the traversed object.
	data := func(object *goja.Object, key string) goja.Value {
		if fast {
			value, found, accessor := object.CanaryOwnPropertyData(key)
			if accessor {
				reject()
			}
			if !found {
				return goja.Undefined()
			}
			return value
		}
		d, err := descriptor(goja.Undefined(), object, vm.ToValue(key))
		if err != nil {
			reject()
		}
		if goja.IsUndefined(d) {
			return goja.Undefined()
		}
		trusted, ok := d.(*goja.Object)
		if !ok {
			reject()
		}
		present, err := own(goja.Undefined(), trusted, valueKey)
		if err != nil || !present.ToBoolean() {
			reject()
		}
		return trusted.Get("value")
	}
	return func(call goja.FunctionCall) goja.Value {
		if active || cancelled() {
			reject()
		}
		active = true
		defer func() {
			active = false
			if thrown := recover(); thrown != nil {
				switch thrown.(type) {
				case goja.Value, *goja.Exception:
					panic(vm.NewTypeError(privateFiniteError))
				default:
					panic(thrown)
				}
			}
		}()
		fast = true
		// Preserve the existing descriptor-construction behavior if its trusted
		// prototype has synthetic fields; public JSONata cannot install them.
		for _, key := range []string{"value", "writable", "enumerable", "configurable", "get", "set"} {
			_, found, _ := prototype.CanaryOwnPropertyData(key)
			if found {
				fast = false
				break
			}
		}
		remaining := 65536
		identities := call.Argument(1)
		var visit func(goja.Value, int)
		visit = func(value goja.Value, depth int) {
			if cancelled() || depth > 128 || remaining == 0 {
				reject()
			}
			remaining--
			object, ok := value.(*goja.Object)
			if !ok {
				// Numeric conversion is reached only for a known primitive
				// number, after objects and all other primitive kinds exclude.
				if goja.IsNumber(value) {
					number := value.ToFloat()
					if math.IsNaN(number) || math.IsInf(number, 0) {
						reject()
					}
				}
				return
			}
			// This pinned public API returns metadata only. Reject proxies,
			// including revoked and callable ones, before all other operations.
			if object.ExportType() == proxyType {
				reject()
			}
			if _, callable := goja.AssertFunction(object); callable {
				return
			}
			opaque, err := has(identities, object)
			if err != nil {
				reject()
			}
			if opaque.ToBoolean() {
				return
			}

			if object.ClassName() == "Array" {
				length := data(object, "length")
				if !goja.IsNumber(length) {
					reject()
				}
				count := length.ToInteger()
				if count < 0 || count > int64(remaining) || count > 0 && depth == 128 {
					reject()
				}
				for index := int64(0); index < count; index++ {
					if fast {
						value, found, accessor := object.CanaryOwnIndexData(uint32(index))
						if accessor {
							reject()
						}
						if !found {
							value = goja.Undefined()
						}
						visit(value, depth+1)
					} else {
						visit(data(object, strconv.FormatInt(index, 10)), depth+1)
					}
				}
			} else {
				// Own enumerable keys, same domain as Object.keys in C1/R2.
				// Proxies have already been rejected; accessors aren't read.
				keys := object.Keys()
				if len(keys) > remaining || len(keys) > 0 && depth == 128 {
					reject()
				}
				for _, key := range keys {
					visit(data(object, key), depth+1)
				}
			}
		}
		value := call.Argument(0)
		visit(value, 0)
		return value
	}
}
