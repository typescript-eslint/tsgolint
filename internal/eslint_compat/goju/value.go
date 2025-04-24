package goju

import (
	"fmt"
	"hash/maphash"
	"math"
	"math/big"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/dop251/goja/ftoa"
	"github.com/dop251/goja/unistring"
)

var (
	// Not goroutine-safe, do not use for anything other than package level init
	pkgHasher maphash.Hash

	hashFalse = randomHash()
	hashTrue  = randomHash()
	hashNull  = randomHash()
	hashUndef = randomHash()
)

// Not goroutine-safe, do not use for anything other than package level init
func randomHash() uint64 {
	pkgHasher.WriteByte(0)
	return pkgHasher.Sum64()
}

var (
	valueFalse    Value = valueBool(false)
	valueTrue     Value = valueBool(true)
	_null         Value = valueNull{}
	_NaN          Value = valueFloat(math.NaN())
	_positiveInf  Value = valueFloat(math.Inf(+1))
	_negativeInf  Value = valueFloat(math.Inf(-1))
	_positiveZero Value = valueInt(0)
	negativeZero        = math.Float64frombits(0 | (1 << 63))
	_negativeZero Value = valueFloat(negativeZero)
	_epsilon            = valueFloat(2.2204460492503130808472633361816e-16)
	_undefined    Value = valueUndefined{}
)

var (
	reflectTypeInt      = reflect.TypeOf(int64(0))
	reflectTypeBool     = reflect.TypeOf(false)
	reflectTypeNil      = reflect.TypeOf(nil)
	reflectTypeFloat    = reflect.TypeOf(float64(0))
	reflectTypeMap      = reflect.TypeOf(map[string]interface{}{})
	reflectTypeArray    = reflect.TypeOf([]interface{}{})
	reflectTypeArrayPtr = reflect.TypeOf((*[]interface{})(nil))
	reflectTypeString   = reflect.TypeOf("")
	reflectTypeFunc     = reflect.TypeOf((func(FunctionCall) Value)(nil))
	reflectTypeError    = reflect.TypeOf((*error)(nil)).Elem()
)

var intCache [256]Value

type Value any

// Value represents an ECMAScript value.
//
// Export returns a "plain" Go value which type depends on the type of the Value.
//
// For integer numbers it's int64.
//
// For any other numbers (including Infinities, NaN and negative zero) it's float64.
//
// For string it's a string. Note that unicode strings are converted into UTF-8 with invalid code points replaced with utf8.RuneError.
//
// For boolean it's bool.
//
// For null and undefined it's nil.
//
// For Object it depends on the Object type, see Object.Export() for more details.
type OldValue interface {
	ToInteger() int64
	toString() String
	string() unistring.String
	ToString() Value
	String() string
	ToFloat() float64
	ToNumber() Value
	ToBoolean() bool
	ToObject(*Runtime) *Object
	SameAs(Value) bool
	Equals(Value) bool
	StrictEquals(Value) bool
	Export() interface{}
	ExportType() reflect.Type

	baseObject(r *Runtime) *Object

	hash(hasher *maphash.Hash) uint64
}

type valueContainer interface {
	toValue(*Runtime) Value
}

type typeError string
type rangeError string
type referenceError string
type syntaxError string

type valueInt int64
type valueFloat float64
type valueBool bool
type valueNull struct{}
type valueUndefined struct {
	valueNull
}

// *Symbol is a Value containing ECMAScript Symbol primitive. Symbols must only be created
// using NewSymbol(). Zero values and copying of values (i.e. *s1 = *s2) are not permitted.
// Well-known Symbols can be accessed using Sym* package variables (SymIterator, etc...)
// Symbols can be shared by multiple Runtimes.
type Symbol struct {
	h    uintptr
	desc String
}

type valueUnresolved struct {
	r   *Runtime
	ref unistring.String
}

type memberUnresolved struct {
	valueUnresolved
}

type valueProperty struct {
	value        Value
	writable     bool
	configurable bool
	enumerable   bool
	accessor     bool
	getterFunc   *Object
	setterFunc   *Object
}

var (
	errAccessBeforeInit = referenceError("Cannot access a variable before initialization")
	errAssignToConst    = typeError("Assignment to constant variable.")
	errMixBigIntType    = typeError("Cannot mix BigInt and other types, use explicit conversions")
)

func propGetter(o Value, v Value, r *Runtime) *Object {
	if v == _undefined {
		return nil
	}
	if obj, ok := v.(*Object); ok {
		if _, ok := obj.self.assertCallable(); ok {
			return obj
		}
	}
	r.typeErrorResult(true, "Getter must be a function: %s", ValueToJsString(v))
	return nil
}

func propSetter(o Value, v Value, r *Runtime) *Object {
	if v == _undefined {
		return nil
	}
	if obj, ok := v.(*Object); ok {
		if _, ok := obj.self.assertCallable(); ok {
			return obj
		}
	}
	r.typeErrorResult(true, "Setter must be a function: %s", ValueToJsString(v))
	return nil
}

func fToStr(num float64, mode ftoa.FToStrMode, prec int) string {
	var buf1 [128]byte
	return string(ftoa.FToStr(num, mode, prec, buf1[:0]))
}

func (i valueInt) ToInteger() int64 {
	return int64(i)
}

func (i valueInt) toString() String {
	return asciiString(i.String())
}

func (i valueInt) string() unistring.String {
	return unistring.String(i.String())
}

func (i valueInt) ToString() Value {
	return i
}

func (i valueInt) String() string {
	return strconv.FormatInt(int64(i), 10)
}

func (i valueInt) ToFloat() float64 {
	return float64(i)
}

func (i valueInt) ToBoolean() bool {
	return i != 0
}

func (i valueInt) ToObject(r *Runtime) *Object {
	return r.newPrimitiveObject(i, r.getNumberPrototype(), classNumber)
}

func (i valueInt) ToNumber() Value {
	return i
}

func (i valueInt) SameAs(other Value) bool {
	return i == other
}

func (i valueInt) Equals(other Value) bool {
	switch o := other.(type) {
	case valueInt:
		return i == o
	case *valueBigInt:
		return (*big.Int)(o).Cmp(big.NewInt(int64(i))) == 0
	case valueFloat:
		return float64(i) == float64(o)
	case String:
		return ValueEquals(ValueToNumber(o), i)
	case valueBool:
		return int64(i) == ValueToInteger(o)
	case *Object:
		return ValueEquals(i, o.toPrimitive())
	}

	return false
}

func (i valueInt) StrictEquals(other Value) bool {
	switch o := other.(type) {
	case valueInt:
		return i == o
	case valueFloat:
		return float64(i) == float64(o)
	}

	return false
}

func (i valueInt) baseObject(r *Runtime) *Object {
	return r.getNumberPrototype()
}

func (i valueInt) Export() interface{} {
	return int64(i)
}

func (i valueInt) ExportType() reflect.Type {
	return reflectTypeInt
}

func (i valueInt) hash(*maphash.Hash) uint64 {
	return uint64(i)
}

func (b valueBool) ToInteger() int64 {
	if b {
		return 1
	}
	return 0
}

func (b valueBool) toString() String {
	if b {
		return stringTrue
	}
	return stringFalse
}

func (b valueBool) ToString() Value {
	return b
}

func (b valueBool) String() string {
	if b {
		return "true"
	}
	return "false"
}

func (b valueBool) string() unistring.String {
	return unistring.String(b.String())
}

func (b valueBool) ToFloat() float64 {
	if b {
		return 1.0
	}
	return 0
}

func (b valueBool) ToBoolean() bool {
	return bool(b)
}

func (b valueBool) ToObject(r *Runtime) *Object {
	return r.newPrimitiveObject(b, r.getBooleanPrototype(), "Boolean")
}

func (b valueBool) ToNumber() Value {
	return valueInt(Bool2int(bool(b)))
}

func (b valueBool) SameAs(other Value) bool {
	if other, ok := other.(valueBool); ok {
		return b == other
	}
	return false
}

func (b valueBool) Equals(other Value) bool {
	if o, ok := other.(valueBool); ok {
		return b == o
	}

	if b {
		return ValueEquals(other, intToValue(1))
	} else {
		return ValueEquals(other, intToValue(0))
	}

}

func (b valueBool) StrictEquals(other Value) bool {
	if other, ok := other.(valueBool); ok {
		return b == other
	}
	return false
}

func (b valueBool) baseObject(r *Runtime) *Object {
	return r.getBooleanPrototype()
}

func (b valueBool) Export() interface{} {
	return bool(b)
}

func (b valueBool) ExportType() reflect.Type {
	return reflectTypeBool
}

func (b valueBool) hash(*maphash.Hash) uint64 {
	if b {
		return hashTrue
	}

	return hashFalse
}

func (n valueNull) ToInteger() int64 {
	return 0
}

func (n valueNull) toString() String {
	return stringNull
}

func (n valueNull) string() unistring.String {
	return ValueToUnistring(stringNull)
}

func (n valueNull) ToString() Value {
	return n
}

func (n valueNull) String() string {
	return "null"
}

func (u valueUndefined) toString() String {
	return stringUndefined
}

func (u valueUndefined) ToString() Value {
	return u
}

func (u valueUndefined) String() string {
	return "undefined"
}

func (u valueUndefined) string() unistring.String {
	return "undefined"
}

func (u valueUndefined) ToNumber() Value {
	return _NaN
}

func (u valueUndefined) SameAs(other Value) bool {
	_, same := other.(valueUndefined)
	return same
}

func (u valueUndefined) StrictEquals(other Value) bool {
	_, same := other.(valueUndefined)
	return same
}

func (u valueUndefined) ToFloat() float64 {
	return math.NaN()
}

func (u valueUndefined) hash(*maphash.Hash) uint64 {
	return hashUndef
}

func (n valueNull) ToFloat() float64 {
	return 0
}

func (n valueNull) ToBoolean() bool {
	return false
}

func (n valueNull) ToObject(r *Runtime) *Object {
	r.typeErrorResult(true, "Cannot convert undefined or null to object")
	return nil
	//return r.newObject()
}

func (n valueNull) ToNumber() Value {
	return intToValue(0)
}

func (n valueNull) SameAs(other Value) bool {
	_, same := other.(valueNull)
	return same
}

func (n valueNull) Equals(other Value) bool {
	switch other.(type) {
	case valueUndefined, valueNull:
		return true
	}
	return false
}

func (n valueNull) StrictEquals(other Value) bool {
	_, same := other.(valueNull)
	return same
}

func (n valueNull) baseObject(*Runtime) *Object {
	return nil
}

func (n valueNull) Export() interface{} {
	return nil
}

func (n valueNull) ExportType() reflect.Type {
	return reflectTypeNil
}

func (n valueNull) hash(*maphash.Hash) uint64 {
	return hashNull
}

func (p *valueProperty) ToInteger() int64 {
	return 0
}

func (p *valueProperty) toString() String {
	return stringEmpty
}

func (p *valueProperty) string() unistring.String {
	return ""
}

func (p *valueProperty) ToString() Value {
	return _undefined
}

func (p *valueProperty) String() string {
	return ""
}

func (p *valueProperty) ToFloat() float64 {
	return math.NaN()
}

func (p *valueProperty) ToBoolean() bool {
	return false
}

func (p *valueProperty) ToObject(*Runtime) *Object {
	return nil
}

func (p *valueProperty) ToNumber() Value {
	return nil
}

func (p *valueProperty) isWritable() bool {
	return p.writable || p.setterFunc != nil
}

func (p *valueProperty) get(this Value) Value {
	if p.getterFunc == nil {
		if p.value != nil {
			return p.value
		}
		return _undefined
	}
	call, _ := p.getterFunc.self.assertCallable()
	return call(FunctionCall{
		This: this,
	})
}

func (p *valueProperty) set(this, v Value) {
	if p.setterFunc == nil {
		p.value = v
		return
	}
	call, _ := p.setterFunc.self.assertCallable()
	call(FunctionCall{
		This:      this,
		Arguments: []Value{v},
	})
}

func (p *valueProperty) SameAs(other Value) bool {
	if otherProp, ok := other.(*valueProperty); ok {
		return p == otherProp
	}
	return false
}

func (p *valueProperty) Equals(Value) bool {
	return false
}

func (p *valueProperty) StrictEquals(Value) bool {
	return false
}

func (p *valueProperty) baseObject(r *Runtime) *Object {
	r.typeErrorResult(true, "BUG: baseObject() is called on valueProperty") // TODO error message
	return nil
}

func (p *valueProperty) Export() interface{} {
	panic("Cannot export valueProperty")
}

func (p *valueProperty) ExportType() reflect.Type {
	panic("Cannot export valueProperty")
}

func (p *valueProperty) hash(*maphash.Hash) uint64 {
	panic("valueProperty should never be used in maps or sets")
}

func floatToIntClip(n float64) int64 {
	switch {
	case math.IsNaN(n):
		return 0
	case n >= math.MaxInt64:
		return math.MaxInt64
	case n <= math.MinInt64:
		return math.MinInt64
	}
	return int64(n)
}

func (f valueFloat) ToInteger() int64 {
	return floatToIntClip(float64(f))
}

func (f valueFloat) toString() String {
	return asciiString(f.String())
}

func (f valueFloat) string() unistring.String {
	return unistring.String(f.String())
}

func (f valueFloat) ToString() Value {
	return f
}

func (f valueFloat) String() string {
	return fToStr(float64(f), ftoa.ModeStandard, 0)
}

func (f valueFloat) ToFloat() float64 {
	return float64(f)
}

func (f valueFloat) ToBoolean() bool {
	return float64(f) != 0.0 && !math.IsNaN(float64(f))
}

func (f valueFloat) ToObject(r *Runtime) *Object {
	return r.newPrimitiveObject(f, r.getNumberPrototype(), "Number")
}

func (f valueFloat) ToNumber() Value {
	return f
}

func (f valueFloat) SameAs(other Value) bool {
	switch o := other.(type) {
	case valueFloat:
		this := float64(f)
		o1 := float64(o)
		if math.IsNaN(this) && math.IsNaN(o1) {
			return true
		} else {
			ret := this == o1
			if ret && this == 0 {
				ret = math.Signbit(this) == math.Signbit(o1)
			}
			return ret
		}
	case valueInt:
		this := float64(f)
		ret := this == float64(o)
		if ret && this == 0 {
			ret = !math.Signbit(this)
		}
		return ret
	}

	return false
}

func (f valueFloat) Equals(other Value) bool {
	switch o := other.(type) {
	case valueFloat:
		return f == o
	case valueInt:
		return float64(f) == float64(o)
	case *valueBigInt:
		if IsInfinity(f) || math.IsNaN(float64(f)) {
			return false
		}
		if f := big.NewFloat(float64(f)); f.IsInt() {
			i, _ := f.Int(nil)
			return (*big.Int)(o).Cmp(i) == 0
		}
		return false
	case String, valueBool:
		return float64(f) == ValueToFloat(o)
	case *Object:
		return ValueEquals(f, o.toPrimitive())
	}

	return false
}

func (f valueFloat) StrictEquals(other Value) bool {
	switch o := other.(type) {
	case valueFloat:
		return f == o
	case valueInt:
		return float64(f) == float64(o)
	}

	return false
}

func (f valueFloat) baseObject(r *Runtime) *Object {
	return r.getNumberPrototype()
}

func (f valueFloat) Export() interface{} {
	return float64(f)
}

func (f valueFloat) ExportType() reflect.Type {
	return reflectTypeFloat
}

func (f valueFloat) hash(*maphash.Hash) uint64 {
	if f == _negativeZero {
		return 0
	}
	return math.Float64bits(float64(f))
}

func (o *Object) ToInteger() int64 {
	return ValueToInteger(ValueToNumber(o.toPrimitiveNumber()))
}

func (o *Object) toString() String {
	return ValueToJsString(o.toPrimitiveString())
}

func (o *Object) string() unistring.String {
	return ValueToUnistring(o.toPrimitiveString())
}

func (o *Object) ToString() Value {
	return ValueToStringValue(o.toPrimitiveString())
}

func (o *Object) String() string {
	return ValueToString(o.toPrimitiveString())
}

func (o *Object) ToFloat() float64 {
	return ValueToFloat(o.toPrimitiveNumber())
}

func (o *Object) ToBoolean() bool {
	return true
}

func (o *Object) ToObject(*Runtime) *Object {
	return o
}

func (o *Object) ToNumber() Value {
	return ValueToNumber(o.toPrimitiveNumber())
}

func (o *Object) SameAs(other Value) bool {
	return ValueStrictEquals(o, other)
}

func (o *Object) Equals(other Value) bool {
	if other, ok := other.(*Object); ok {
		return o == other || o.self.equal(other.self)
	}

	switch o1 := other.(type) {
	case valueInt, valueFloat, *valueBigInt, String, *Symbol:
		return ValueEquals(o.toPrimitive(), other)
	case valueBool:
		return ValueEquals(o, ValueToNumber(o1))
	}

	return false
}

func (o *Object) StrictEquals(other Value) bool {
	if other, ok := other.(*Object); ok {
		return o == other || o != nil && other != nil && o.self.equal(other.self)
	}
	return false
}

func (o *Object) baseObject(*Runtime) *Object {
	return o
}

// Export the Object to a plain Go type.
// If the Object is a wrapped Go value (created using ToValue()) returns the original value.
//
// If the Object is a function, returns func(FunctionCall) Value. Note that exceptions thrown inside the function
// result in panics, which can also leave the Runtime in an unusable state. Therefore, these values should only
// be used inside another ES function implemented in Go. For calling a function from Go, use AssertFunction() or
// Runtime.ExportTo() as described in the README.
//
// For a Map, returns the list of entries as [][2]interface{}.
//
// For a Set, returns the list of elements as []interface{}.
//
// For a Proxy, returns Proxy.
//
// For a Promise, returns Promise.
//
// For a DynamicObject or a DynamicArray, returns the underlying handler.
//
// For typed arrays it returns a slice of the corresponding type backed by the original data (i.e. it does not copy).
//
// For an untyped array, returns its items exported into a newly created []interface{}.
//
// In all other cases returns own enumerable non-symbol properties as map[string]interface{}.
//
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) Export() interface{} {
	return o.self.export(&objectExportCtx{})
}

// ExportType returns the type of the value that is returned by Export().
func (o *Object) ExportType() reflect.Type {
	return o.self.exportType()
}

func (o *Object) hash(*maphash.Hash) uint64 {
	return o.getId()
}

// Get an object's property by name.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) Get(name string) Value {
	return o.self.getStr(unistring.NewFromString(name), nil)
}

// GetSymbol returns the value of a symbol property. Use one of the Sym* values for well-known
// symbols (such as SymIterator, SymToStringTag, etc...).
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) GetSymbol(sym *Symbol) Value {
	return o.self.getSym(sym, nil)
}

// Keys returns a list of Object's enumerable keys.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) Keys() (keys []string) {
	iter := &enumerableIter{
		o:       o,
		wrapped: o.self.iterateStringKeys(),
	}
	for item, next := iter.next(); next != nil; item, next = next() {
		keys = append(keys, ValueToString(item.name))
	}

	return
}

// GetOwnPropertyNames returns a list of all own string properties of the Object, similar to Object.getOwnPropertyNames()
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) GetOwnPropertyNames() (keys []string) {
	for item, next := o.self.iterateStringKeys()(); next != nil; item, next = next() {
		keys = append(keys, ValueToString(item.name))
	}

	return
}

// Symbols returns a list of Object's enumerable symbol properties.
// This method will panic with an *Exception if a JavaScript exception is thrown in the process. Use Runtime.Try to catch these.
func (o *Object) Symbols() []*Symbol {
	symbols := o.self.symbols(false, nil)
	ret := make([]*Symbol, len(symbols))
	for i, sym := range symbols {
		ret[i], _ = sym.(*Symbol)
	}
	return ret
}

// DefineDataProperty is a Go equivalent of Object.defineProperty(o, name, {value: value, writable: writable,
// configurable: configurable, enumerable: enumerable})
func (o *Object) DefineDataProperty(name string, value Value, writable, configurable, enumerable Flag) error {
	return o.runtime.try(func() {
		o.self.defineOwnPropertyStr(unistring.NewFromString(name), PropertyDescriptor{
			Value:        value,
			Writable:     writable,
			Configurable: configurable,
			Enumerable:   enumerable,
		}, true)
	})
}

// DefineAccessorProperty is a Go equivalent of Object.defineProperty(o, name, {get: getter, set: setter,
// configurable: configurable, enumerable: enumerable})
func (o *Object) DefineAccessorProperty(name string, getter, setter Value, configurable, enumerable Flag) error {
	return o.runtime.try(func() {
		o.self.defineOwnPropertyStr(unistring.NewFromString(name), PropertyDescriptor{
			Getter:       getter,
			Setter:       setter,
			Configurable: configurable,
			Enumerable:   enumerable,
		}, true)
	})
}

// DefineDataPropertySymbol is a Go equivalent of Object.defineProperty(o, name, {value: value, writable: writable,
// configurable: configurable, enumerable: enumerable})
func (o *Object) DefineDataPropertySymbol(name *Symbol, value Value, writable, configurable, enumerable Flag) error {
	return o.runtime.try(func() {
		o.self.defineOwnPropertySym(name, PropertyDescriptor{
			Value:        value,
			Writable:     writable,
			Configurable: configurable,
			Enumerable:   enumerable,
		}, true)
	})
}

// DefineAccessorPropertySymbol is a Go equivalent of Object.defineProperty(o, name, {get: getter, set: setter,
// configurable: configurable, enumerable: enumerable})
func (o *Object) DefineAccessorPropertySymbol(name *Symbol, getter, setter Value, configurable, enumerable Flag) error {
	return o.runtime.try(func() {
		o.self.defineOwnPropertySym(name, PropertyDescriptor{
			Getter:       getter,
			Setter:       setter,
			Configurable: configurable,
			Enumerable:   enumerable,
		}, true)
	})
}

func (o *Object) Set(name string, value interface{}) error {
	return o.runtime.try(func() {
		o.self.setOwnStr(unistring.NewFromString(name), o.runtime.ToValue(value), true)
	})
}

func (o *Object) SetSymbol(name *Symbol, value interface{}) error {
	return o.runtime.try(func() {
		o.self.setOwnSym(name, o.runtime.ToValue(value), true)
	})
}

func (o *Object) Delete(name string) error {
	return o.runtime.try(func() {
		o.self.deleteStr(unistring.NewFromString(name), true)
	})
}

func (o *Object) DeleteSymbol(name *Symbol) error {
	return o.runtime.try(func() {
		o.self.deleteSym(name, true)
	})
}

// Prototype returns the Object's prototype, same as Object.getPrototypeOf(). If the prototype is null
// returns nil.
func (o *Object) Prototype() *Object {
	return o.self.proto()
}

// SetPrototype sets the Object's prototype, same as Object.setPrototypeOf(). Setting proto to nil
// is an equivalent of Object.setPrototypeOf(null).
func (o *Object) SetPrototype(proto *Object) error {
	return o.runtime.try(func() {
		o.self.setProto(proto, true)
	})
}

// MarshalJSON returns JSON representation of the Object. It is equivalent to JSON.stringify(o).
// Note, this implements json.Marshaler so that json.Marshal() can be used without the need to Export().
func (o *Object) MarshalJSON() ([]byte, error) {
	ctx := _builtinJSON_stringifyContext{
		r: o.runtime,
	}
	ex := o.runtime.vm.try(func() {
		if !ctx.do(o) {
			ctx.buf.WriteString("null")
		}
	})
	if ex != nil {
		return nil, ex
	}
	return ctx.buf.Bytes(), nil
}

// UnmarshalJSON implements the json.Unmarshaler interface. It is added to compliment MarshalJSON, because
// some alternative JSON encoders refuse to use MarshalJSON unless UnmarshalJSON is also present.
// It is a no-op and always returns nil.
func (o *Object) UnmarshalJSON([]byte) error {
	return nil
}

// ClassName returns the class name
func (o *Object) ClassName() string {
	return o.self.className()
}

func (o valueUnresolved) throw() {
	o.r.throwReferenceError(o.ref)
}

func (o valueUnresolved) ToInteger() int64 {
	o.throw()
	return 0
}

func (o valueUnresolved) toString() String {
	o.throw()
	return nil
}

func (o valueUnresolved) string() unistring.String {
	o.throw()
	return ""
}

func (o valueUnresolved) ToString() Value {
	o.throw()
	return nil
}

func (o valueUnresolved) String() string {
	o.throw()
	return ""
}

func (o valueUnresolved) ToFloat() float64 {
	o.throw()
	return 0
}

func (o valueUnresolved) ToBoolean() bool {
	o.throw()
	return false
}

func (o valueUnresolved) ToObject(*Runtime) *Object {
	o.throw()
	return nil
}

func (o valueUnresolved) ToNumber() Value {
	o.throw()
	return nil
}

func (o valueUnresolved) SameAs(Value) bool {
	o.throw()
	return false
}

func (o valueUnresolved) Equals(Value) bool {
	o.throw()
	return false
}

func (o valueUnresolved) StrictEquals(Value) bool {
	o.throw()
	return false
}

func (o valueUnresolved) baseObject(*Runtime) *Object {
	o.throw()
	return nil
}

func (o valueUnresolved) Export() interface{} {
	o.throw()
	return nil
}

func (o valueUnresolved) ExportType() reflect.Type {
	o.throw()
	return nil
}

func (o valueUnresolved) hash(*maphash.Hash) uint64 {
	o.throw()
	return 0
}

func (s *Symbol) ToInteger() int64 {
	panic(typeError("Cannot convert a Symbol value to a number"))
}

func (s *Symbol) toString() String {
	panic(typeError("Cannot convert a Symbol value to a string"))
}

func (s *Symbol) ToString() Value {
	return s
}

func (s *Symbol) String() string {
	if s.desc != nil {
		return ValueToString(s.desc)
	}
	return ""
}

func (s *Symbol) string() unistring.String {
	if s.desc != nil {
		return ValueToUnistring(s.desc)
	}
	return ""
}

func (s *Symbol) ToFloat() float64 {
	panic(typeError("Cannot convert a Symbol value to a number"))
}

func (s *Symbol) ToNumber() Value {
	panic(typeError("Cannot convert a Symbol value to a number"))
}

func (s *Symbol) ToBoolean() bool {
	return true
}

func (s *Symbol) ToObject(r *Runtime) *Object {
	return ValueBaseObject(r, s)
}

func (s *Symbol) SameAs(other Value) bool {
	if s1, ok := other.(*Symbol); ok {
		return s == s1
	}
	return false
}

func (s *Symbol) Equals(o Value) bool {
	switch o := o.(type) {
	case *Object:
		return ValueEquals(s, o.toPrimitive())
	}
	return ValueSameAs(s, o)
}

func (s *Symbol) StrictEquals(o Value) bool {
	return ValueSameAs(s, o)
}

func (s *Symbol) Export() interface{} {
	return ValueToString(s)
}

func (s *Symbol) ExportType() reflect.Type {
	return reflectTypeString
}

func (s *Symbol) baseObject(r *Runtime) *Object {
	return r.newPrimitiveObject(s, r.getSymbolPrototype(), classObject)
}

func (s *Symbol) hash(*maphash.Hash) uint64 {
	return uint64(s.h)
}

func exportValue(v Value, ctx *objectExportCtx) interface{} {
	if obj, ok := v.(*Object); ok {
		return obj.self.export(ctx)
	}
	return ValueExport(v)
}

func newSymbol(s String) *Symbol {
	r := &Symbol{
		desc: s,
	}
	// This may need to be reconsidered in the future.
	// Depending on changes in Go's allocation policy and/or introduction of a compacting GC
	// this may no longer provide sufficient dispersion. The alternative, however, is a globally
	// synchronised random generator/hasher/sequencer and I don't want to go down that route just yet.
	r.h = uintptr(unsafe.Pointer(r))
	return r
}

func NewSymbol(s string) *Symbol {
	return newSymbol(newStringValue(s))
}

func (s *Symbol) descriptiveString() String {
	desc := s.desc
	if desc == nil {
		desc = stringEmpty
	}
	return asciiString("Symbol(").Concat(desc).Concat(asciiString(")"))
}

func funcName(prefix string, n Value) String {
	var b StringBuilder
	b.WriteString(asciiString(prefix))
	if sym, ok := n.(*Symbol); ok {
		if sym.desc != nil {
			b.WriteRune('[')
			b.WriteString(sym.desc)
			b.WriteRune(']')
		}
	} else {
		b.WriteString(ValueToJsString(n))
	}
	return b.String()
}

func newTypeError(args ...interface{}) typeError {
	msg := ""
	if len(args) > 0 {
		f, _ := args[0].(string)
		msg = fmt.Sprintf(f, args[1:]...)
	}
	return typeError(msg)
}

func typeErrorResult(throw bool, args ...interface{}) {
	if throw {
		panic(newTypeError(args...))
	}

}

func init() {
	for i := 0; i < 256; i++ {
		intCache[i] = valueInt(i - 256)
	}
}

/////

func ValueToInteger(v any) int64 {
	if v, ok := v.(OldValue); ok {
		return v.ToInteger()
	}
	return 0
}
func ValueToJsString(v any) String {
	if v, ok := v.(OldValue); ok {
		return v.toString()
	}
	return stringEmptyPlaceholder
}
func ValueToUnistring(v any) unistring.String {
	if v, ok := v.(OldValue); ok {
		return v.string()
	}
	return unistring.String(ValueToString(v))
}
func ValueToString(v any) string {
	if v, ok := v.(OldValue); ok {
		return v.String()
	} 
	return "<$$$>"
}
func ValueToStringValue(v any) Value {
	if v, ok := v.(OldValue); ok {
		return v.ToString()
	}
	return _undefined
}
func ValueToFloat(v any) float64 {
	if v, ok := v.(OldValue); ok {
		return v.ToFloat()
	}
	return 0
}
func ValueToNumber(v any) Value {
	if v, ok := v.(OldValue); ok {
		return v.ToNumber()
	}
	// TODO: pass *Runtime to all ValueTo* functions so that we can fallback to
	// default toValue resolution here
	return valueInt(0)
}
func ValueToBoolean(v any) bool {
	if v, ok := v.(OldValue); ok {
		return v.ToBoolean()
	}
	return true
}
func ValueToObject(r *Runtime, v any) *Object {
	if v, ok := v.(OldValue); ok {
		return v.ToObject(r)
	}
	panic(r.NewTypeError("Couldn't convert %T to object", v))
}
func ValueSameAs(left, right any) bool {
	if left, ok := left.(OldValue); ok {
		if right, ok := right.(OldValue); ok {
			return left.SameAs(right)
		}
	}
	return left == right
}
func ValueEquals(left, right any) bool {
	if left, ok := left.(OldValue); ok {
		if right, ok := right.(OldValue); ok {
			return left.Equals(right)
		}
	}
	return left == right
}
func ValueStrictEquals(left, right any) bool {
	if left, ok := left.(OldValue); ok {
		if right, ok := right.(OldValue); ok {
			return left.StrictEquals(right)
		}
	}
	return left == right
}
func ValueExport(v any) any {
	if v, ok := v.(OldValue); ok {
		return v.Export()
	}
	return v
}
func ValueExportType(v any) reflect.Type {
	if v, ok := v.(OldValue); ok {
		return v.ExportType()
	}
	return reflect.TypeOf(v)
}
func ValueGoObjectGetAnyKey(runtime *Runtime, v any, prop Value) (any, bool) {
	switch prop.(type) {
	case valueInt, *Symbol:
		return nil, false
	}
	p := string(ValueToUnistring(prop))
	return ValueGoObjectGet(runtime, v, p)
}

func ValueGoObjectGet(runtime *Runtime, v any, prop string) (any, bool) {
	if _, ok := v.(OldValue); ok {
		return nil, false
	}
	if i, ok := v.(interface { JsInterop(prop string) any }); ok {
		res := i.JsInterop(prop)
		if res == nil {
			return _undefined, true
		}
		return runtime.ToValue(res), true
	}
	{
		val := reflect.ValueOf(v)
		if val.Kind() == reflect.Pointer {
			val = val.Elem()
		}
		if !val.IsValid() {
			return nil, false
		}
		if val.Kind() == reflect.Interface {
			val = val.Elem()
		}
		if val.Kind() == reflect.Struct {
			t := val.Type()
			for i := range t.NumField() {
				n :=  t.Field(i).Name
				if (n[0] != prop[0] && n[0] + 32 != prop[0]) || n[1:] != prop[1:]{
					continue
				}
				f := val.Field(i)
				if f.Kind() == reflect.Interface {
					f = f.Elem()
				}
				if f.Kind() == reflect.Invalid {
					return _undefined, true
				}
				return runtime.toValue(f.Interface(), f), true
			}
		}
	}

	val := reflect.ValueOf(v)
	t := val.Type()
	methodsType := t
	// Always use pointer type for non-interface values to be able to access both methods defined on
	// the literal type and on the pointer.
	if val.Kind() != reflect.Interface && val.Kind() != reflect.Pointer {
		methodsType = reflect.PointerTo(t)
	}
	numMethod := methodsType.NumMethod()
	// Container values and values that have at least one method defined on the pointer type
	// need to be addressable.
	if !val.CanAddr() && val.Kind() != reflect.Pointer && (isContainer(val.Kind()) || numMethod > 0) {
		n := reflect.New(t)
		value := n.Elem()
		value.Set(val)
		val = n
		// if value.Kind() != reflect.Ptr {
		// 	o.fieldsValue = value
		// }
	} else if val.CanAddr() {
		val = val.Addr()
	}
	for i := range numMethod {
		n := methodsType.Method(i).Name
		if (n[0] != prop[0] && n[0] + 32 != prop[0]) || n[1:] != prop[1:]{
			continue
		}

		f := val.Method(i)
		return runtime.toValue(f.Interface(), f), true
	}
	return _undefined, true
}
func ValueBaseObject(r *Runtime, v any) *Object {
	if v, ok := v.(OldValue); ok {
		return v.baseObject(r)
	}
	panic(r.NewTypeError("Couldn't convert %T to base object", v))
}

// EmptyInterface describes the layout of a "interface{}" or a "any."
// These are represented differently than non-empty interface, as the first
// word always points to an abi.Type.
type emptyInterface struct {
	Type uintptr
	Data unsafe.Pointer
}

func ValueHash(v any, hash *maphash.Hash) uint64 {
	if v, ok := v.(OldValue); ok {
		return v.hash(hash)
	}
	return uint64(uintptr(((*emptyInterface)(unsafe.Pointer(&v))).Data))
}

func Bool2int(b bool) int {
	// The compiler currently only optimizes this form.
	// See issue 6011.
	var i int
	if b {
		i = 1
	} else {
		i = 0
	}
	return i
}
