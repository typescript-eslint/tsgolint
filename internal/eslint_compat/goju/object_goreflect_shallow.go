package goju

import (
	"reflect"
	"unsafe"

	"github.com/dop251/goja/unistring"
)

type objectGoReflectShallow struct {
	runtime *Runtime
	inner reflect.Value
	orig reflect.Value
}

var _ objectImpl = (*objectGoReflectShallow)(nil)


func (o *objectGoReflectShallow) sortLen() int {
	panic("unimplemented sortLen")
}
func (o *objectGoReflectShallow) sortGet(i int) Value {
	panic("unimplemented sortGet")
}
func (o *objectGoReflectShallow) swap(i int, j int) {
	panic("unimplemented swap")
}

func (o *objectGoReflectShallow) className() string {
	return "GoReflectShallow"
}
func (o *objectGoReflectShallow) typeOf() String {
	return stringObjectC
}
func (o *objectGoReflectShallow) getStr(p unistring.String, receiver Value) Value {
	name := p.String()

	t := o.inner.Type()
	numField := t.NumField()
	for i := 0; i < numField; i++ {
		if o.runtime.fieldNameMapper.FieldName(o.inner.Type(), t.Field(i)) == name {
			f := o.inner.Field(i)
			if f.Kind() == reflect.Interface {
				f = f.Elem()
			}
			if f.Kind() == reflect.Invalid {
				return _null
			}
			return o.runtime.toValue(f.Interface(), f)
		}
	}

	t = o.orig.Type()
	numMethod := t.NumMethod()
	methodsValue := o.inner
	if o.inner.Kind() != reflect.Interface {
		methodsValue = o.inner.Addr()
	}
	for i := 0; i < numMethod ; i++ {
		if o.runtime.fieldNameMapper.MethodName(o.inner.Type(), t.Method(i)) == name {
			f := methodsValue.Method(i)
			return o.runtime.toValue(f.Interface(), f)
		}
	}


	if receiver == nil {
		return o.runtime.globalObject.self.getStr(p, _null)
	}
	return o.runtime.globalObject.self.getStr(p, receiver)
}
func (o *objectGoReflectShallow) getIdx(idx valueInt, receiver Value) Value {
	return o.getStr(ValueToUnistring(idx), receiver)
}
func (o *objectGoReflectShallow) getSym(p *Symbol, receiver Value) Value {
	return _undefined
}

func (o *objectGoReflectShallow) getOwnPropStr(unistring.String) Value {
	panic("unimplemented getOwnPropStr")
}
func (o *objectGoReflectShallow) getOwnPropIdx(valueInt) Value {
	panic("unimplemented getOwnPropIdx")
}
func (o *objectGoReflectShallow) getOwnPropSym(*Symbol) Value {
	return _undefined
}

func (o *objectGoReflectShallow) hasOwnPropertyStr(unistring.String) bool {
	panic("unimplemented hasOwnPropertyStr")
}
func (o *objectGoReflectShallow) hasOwnPropertyIdx(valueInt) bool {
	panic("unimplemented hasOwnPropertyIdx")
}
func (o *objectGoReflectShallow) hasOwnPropertySym(s *Symbol) bool {
	return false
}
			
func (o *objectGoReflectShallow) setOwnStr(p unistring.String, v Value, throw bool) bool {
	o.runtime.typeErrorResult(throw, "setOwnStr: cannot add property %s, shallow object is not extensible", p)
	return false
}
func (o *objectGoReflectShallow) setOwnIdx(p valueInt, v Value, throw bool) bool {
	o.runtime.typeErrorResult(throw, "setOwnIdx: cannot add property %s, shallow object is not extensible", p)
	return false
}
func (o *objectGoReflectShallow) setOwnSym(p *Symbol, v Value, throw bool) bool {
	o.runtime.typeErrorResult(throw, "setOwnSym: cannot add property %s, shallow object is not extensible", p)
	return false
}

func (o *objectGoReflectShallow) setForeignStr(p unistring.String, v, receiver Value, throw bool) (res bool, handled bool) {
	o.runtime.typeErrorResult(throw, "setForeignStr: cannot add property %s, shallow object is not extensible", p)
	return false, true
}
func (o *objectGoReflectShallow) setForeignIdx(p valueInt, v, receiver Value, throw bool) (res bool, handled bool) {
	o.runtime.typeErrorResult(throw, "setForeignIdx: cannot add property %s, shallow object is not extensible", p)
	return false, true
}
func (o *objectGoReflectShallow) setForeignSym(p *Symbol, v, receiver Value, throw bool) (res bool, handled bool) {
	o.runtime.typeErrorResult(throw, "setForeignSym: cannot add property %s, shallow object is not extensible", p)
	return false, true
}

func (o *objectGoReflectShallow) hasPropertyStr(name unistring.String) bool {
	n := name.String()
	t := o.inner.Type()
	numField := t.NumField()
	for i := 0; i < numField; i++ {
		if o.runtime.fieldNameMapper.FieldName(o.inner.Type(), t.Field(i)) == n {
			return true
		}
	}

	t = o.orig.Type()
	numMethod := t.NumMethod()
	for i := 0; i < numMethod ; i++ {
		if o.runtime.fieldNameMapper.MethodName(o.inner.Type(), t.Method(i)) == n {
			return true
		}
	}
	return false
}
func (o *objectGoReflectShallow) hasPropertyIdx(idx valueInt) bool {
	return o.hasPropertyStr(ValueToUnistring(idx))
}
func (o *objectGoReflectShallow) hasPropertySym(s *Symbol) bool {
	return false
}

func (o *objectGoReflectShallow) defineOwnPropertyStr(name unistring.String, desc PropertyDescriptor, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot define property %s, shallow object is not extensible", name)
	return false
}
func (o *objectGoReflectShallow) defineOwnPropertyIdx(name valueInt, desc PropertyDescriptor, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot define property %s, shallow object is not extensible", name)
	return false
}
func (o *objectGoReflectShallow) defineOwnPropertySym(name *Symbol, desc PropertyDescriptor, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot define property %s, shallow object is not extensible", name)
	return false
}

func (o *objectGoReflectShallow) deleteStr(name unistring.String, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot delete property %s, shallow object is not extensible", name)
	return false
}
func (o *objectGoReflectShallow) deleteIdx(name valueInt, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot delete property %s, shallow object is not extensible", name)
	return false
}
func (o *objectGoReflectShallow) deleteSym(name *Symbol, throw bool) bool {
	o.runtime.typeErrorResult(throw, "Cannot delete property %s, shallow object is not extensible", name)
	return false
}

func (o *objectGoReflectShallow) assertCallable() (func(FunctionCall) Value, bool) {
	return nil, false
}
func (o *objectGoReflectShallow) vmCall(vm *vm, _ int) {
	panic(vm.r.NewTypeError("Not a function: objectGoReflectShallow"))
}
func (o *objectGoReflectShallow) assertConstructor() func(args []Value, newTarget *Object) *Object {
	return nil
}
func (o *objectGoReflectShallow) proto() *Object {
	return o.runtime.global.ObjectPrototype
}
func (o *objectGoReflectShallow) setProto(proto *Object, throw bool) bool {
	o.runtime.typeErrorResult(throw, "objectGoReflectShallow is not extensible")
	return false
}
func (o *objectGoReflectShallow) hasInstance(Value) bool {
	panic(o.runtime.NewTypeError("Expecting a function in instanceof check, but got objectGoReflectShallow"))
}
func (o *objectGoReflectShallow) isExtensible() bool {
	return false
}
func (o *objectGoReflectShallow) preventExtensions(bool) bool {
	return true
}

func (o *objectGoReflectShallow) export(ctx *objectExportCtx) interface{} {
	return o.orig.Interface()
}
func (o *objectGoReflectShallow) exportType() reflect.Type {
	return o.orig.Type()
}
func (o *objectGoReflectShallow) exportToMap(m reflect.Value, typ reflect.Type, ctx *objectExportCtx) error {
	panic("unimplemented exportToMap")
}
func (o *objectGoReflectShallow) exportToArrayOrSlice(s reflect.Value, typ reflect.Type, ctx *objectExportCtx) error {
	panic("unimplemented exportToArrayOrSlice")
}
func (o *objectGoReflectShallow) equal(other objectImpl) bool {
	if other, ok := other.(*objectGoReflectShallow); ok {
		return o.inner == other.inner
	}
	return false
}

type goreflectShallowPropIter struct {
	o *objectGoReflectShallow
	idx int
}

func (i *goreflectShallowPropIter) nextField() (propIterItem, iterNextFunc) {
	if i.idx < i.o.inner.NumField() {
		return propIterItem{
			name: newStringValue(i.o.runtime.fieldNameMapper.FieldName(i.o.inner.Type(), i.o.inner.Type().Field(i.idx))),
			enumerable: _ENUM_TRUE,
		}, i.nextField
	}
	return propIterItem{}, nil
}


func (o *objectGoReflectShallow) iterateStringKeys() iterNextFunc {
	i := goreflectShallowPropIter{o: o}
	return i.nextField
}
func (o *objectGoReflectShallow) iterateSymbols() iterNextFunc {
	return func() (propIterItem, iterNextFunc) {
		return propIterItem{}, nil
	}
}
func (o *objectGoReflectShallow) iterateKeys() iterNextFunc {
	return o.iterateStringKeys()
}

func (o *objectGoReflectShallow) stringKeys(all bool, accum []Value) []Value {
	t := o.inner.Type()
	fields := t.NumField()
	for i := 0; i < fields; i++  {
		accum = append(accum, newStringValue(o.runtime.fieldNameMapper.FieldName(t, t.Field(i))))
	}
	return accum
}
func (o *objectGoReflectShallow) symbols(all bool, accum []Value) []Value {
	return accum
}
func (o *objectGoReflectShallow) keys(all bool, accum []Value) []Value {
	return o.stringKeys(all, accum)
}

func (o *objectGoReflectShallow) _putProp(name unistring.String, value Value, writable, enumerable, configurable bool) Value {
	panic("unimplemented _putProp")
}
func (o *objectGoReflectShallow) _putSym(s *Symbol, prop Value) {
	panic("unimplemented _putSym")
}
func (o *objectGoReflectShallow) getPrivateEnv(typ *privateEnvType, create bool) *privateElements {
	panic("unimplemented getPrivateEnv")
}

func (o *objectGoReflectShallow) hash() uint64 {
	return uint64(uintptr(unsafe.Pointer(o.inner.Addr().UnsafePointer())))
}
