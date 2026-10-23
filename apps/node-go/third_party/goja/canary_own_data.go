package goja

import "github.com/dop251/goja/unistring"

// Private candidate extension: inspect own descriptor storage without reading
// an accessor or constructing a JS descriptor object. The caller rejects proxies.
func (o *Object) CanaryOwnPropertyData(name string) (Value, bool, bool) {
	return canaryPropertyData(o.self.getOwnPropStr(unistring.NewFromString(name)))
}
func (o *Object) CanaryOwnIndexData(index uint32) (Value, bool, bool) {
	return canaryPropertyData(o.self.getOwnPropIdx(valueInt(index)))
}
func canaryPropertyData(v Value) (Value, bool, bool) {
	if v == nil {
		return _undefined, false, false
	}
	if p, ok := v.(*valueProperty); ok {
		if p.accessor {
			return _undefined, true, true
		}
		if p.value == nil {
			return _undefined, true, false
		}
		return p.value, true, false
	}
	return v, true, false
}
