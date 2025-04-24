package estree

// TODO: report on semantic errors
// TODO: should we distinguish null and undefined? some nodes have 'null | undefined' properties

import (
	"math/big"
	"reflect"
	"slices"
	"strings"

	gojaParser "github.com/dop251/goja/parser"
	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/scanner"
	"github.com/microsoft/typescript-go/shim/stringutil"
	goja "github.com/typescript-eslint/tsgolint/internal/eslint_compat/goju"
	"github.com/typescript-eslint/tsgolint/internal/utils"
)

type converter struct {
	vm                    *goja.Runtime
	sourceFile            *ast.SourceFile
	esTreeNodeToTSNodeMap map[any]*ast.Node
	tsNodeToESTreeNodeMap map[*ast.Node]any
	allowPattern          bool
}

func ConvertSourceFileToESTree(sourceFile *ast.SourceFile, vm *goja.Runtime) *Program {
	c := converter{
		vm:                    vm,
		sourceFile:            sourceFile,
		esTreeNodeToTSNodeMap: map[any]*ast.Node{},
		tsNodeToESTreeNodeMap: map[*ast.Node]any{},
		allowPattern:          false,
	}

	return c.convertNode(sourceFile.AsNode(), nil).(*Program)
}

func (c *converter) getRange(node *ast.Node) Range {
	return Range{
		c.getNodeStart(node),
		node.End(),
	}
}

func getLineAndCharacterFor(pos int, sourceFile *ast.SourceFile) Position {
	line, character := scanner.GetLineAndCharacterOfPosition(sourceFile, pos)
	return Position{
		Column: character,
		Line:   line + 1,
	}
}

func (c *converter) getLocFor(r Range) SourceLocation {
	return SourceLocation{
		Start: getLineAndCharacterFor(r[0], c.sourceFile),
		End:   getLineAndCharacterFor(r[1], c.sourceFile),
	}
}

func (c *converter) createNode(node *ast.Node, data NodeWithRange) NodeWithRange {
	r := data.GetRange()
	if r[0] == 0 && r[1] == 0 {
		r = c.getRange(node)
		data.setRange(r)
	}
	l := data.GetLoc()
	if l.Start.Line == 0 && l.Start.Column == 0 && l.End.Line == 0 && l.End.Column == 0 {
		data.setLoc(c.getLocFor(r))
	}

	c.esTreeNodeToTSNodeMap[data] = node

	return data
}

func convertChildT[T any](c *converter, child *ast.Node, parent *ast.Node) T {
	res, ok := c.convertChild(child, parent).(T)
	if child != nil && !ok {
		panic("couldn't assert child to T type")
	}
	return res
}
func (c *converter) convertChild(child *ast.Node, parent *ast.Node) NodeWithRange {
	return c.converter(child, parent, false)
}
func (c *converter) convertPattern(child, parent *ast.Node) NodeWithRange {
	return c.converter(child, parent, true)
}

func (c *converter) registerTSNodeInNodeMap(node *ast.Node, result any) {
	if result == nil {
		return
	}
	_, ok := c.tsNodeToESTreeNodeMap[node]
	if !ok {
		c.tsNodeToESTreeNodeMap[node] = result
	}
}

func hasModifier(modifier ast.Kind, node interface{ Modifiers() *ast.ModifierList }) bool {
	modifiers := node.Modifiers()
	if modifiers == nil {
		return false
	}
	for _, m := range modifiers.NodeList.Nodes {
		if m.Kind == modifier {
			return true
		}
	}
	return false
}

func getModifiers(node *ast.Node) []*ast.Node {
	if node.ModifierFlags()&ast.ModifierFlagsModifier == 0 {
		return nil
	}
	modifiers := node.ModifierNodes()
	if modifiers == nil {
		return nil
	}
	return Filter(modifiers, func(n *ast.Node) bool {
		return !ast.IsDecorator(n)
	})
}

func (c *converter) converter(node, parent *ast.Node, allowPattern bool) NodeWithRange {
	if node == nil {
		return nil
	}

	pattern := c.allowPattern
	c.allowPattern = allowPattern

	if parent == nil {
		parent = node.Parent
	}
	result := c.convertNode(node, parent)

	c.registerTSNodeInNodeMap(node, result)

	c.allowPattern = pattern

	return result
}

func (c *converter) convertTypeAnnotation(child, parent *ast.Node) *TSTypeAnnotation {
	if child == nil {
		return nil
	}
	// in FunctionType and ConstructorType typeAnnotation has 2 characters `=>` and in other places is just colon
	offset := 1
	if parent != nil && (ast.IsFunctionTypeNode(parent) || parent.Kind == ast.KindConstructorType) {
		offset = 2
	}
	annotationStartCol := child.Pos() - offset

	result := TSTypeAnnotation{
		Type:           ESTreeKindTSTypeAnnotation,
		TypeAnnotation: c.convertChild(child, nil),
	}
	r := Range{annotationStartCol, child.End()}
	result.setRange(r)
	result.setLoc(c.getLocFor(r))
	return &result
}

func getNamespaceModifiers(node *ast.ModuleDeclaration) []*ast.Node {
	// For following nested namespaces, use modifiers given to the topmost namespace
	//   export declare namespace foo.bar.baz {}

	modifiers := node.ModifierNodes()
	var moduleDeclaration *ast.Node = node.AsNode()
	for (modifiers == nil || len(modifiers) == 0) && ast.IsModuleDeclaration(moduleDeclaration.Parent) {
		parentModifiers := moduleDeclaration.Parent.ModifierNodes()
		if parentModifiers != nil && len(parentModifiers) != 0 {
			modifiers = parentModifiers
		}
		moduleDeclaration = moduleDeclaration.Parent
	}
	return modifiers
}

func (c *converter) getNodeStart(node *ast.Node) int {
	return scanner.GetTokenPosOfNode(node, c.sourceFile, false)
}

func (c *converter) fixExports(node *ast.Node, result NodeWithRange) NodeWithRange {
	isNamespaceNode := ast.IsModuleDeclaration(node) && !ast.IsStringLiteral(node.Name())

	var modifiers []*ast.Node
	if isNamespaceNode {
		modifiers = getNamespaceModifiers(node.AsModuleDeclaration())
	} else {
		modifiers = getModifiers(node)
	}

	if len(modifiers) < 1 || modifiers[0].Kind != ast.KindExportKeyword {
		return result
	}

	/**
	 * Make sure that original node is registered instead of export
	 */
	c.registerTSNodeInNodeMap(node, result)

	exportKeyword := modifiers[0]
	declarationIsDefault := len(modifiers) >= 2 && modifiers[1].Kind == ast.KindDefaultKeyword

	var varTokenRange core.TextRange

	if declarationIsDefault {
		varTokenRange = scanner.GetRangeOfTokenAtPosition(c.sourceFile, modifiers[1].End())
	} else {
		varTokenRange = scanner.GetRangeOfTokenAtPosition(c.sourceFile, exportKeyword.End())
	}

	r := Range{varTokenRange.Pos(), result.GetRange()[1]}
	result.setRange(r)
	result.setLoc(c.getLocFor(r))

	if declarationIsDefault {
		return c.createNode(node, &ExportDefaultDeclaration{
			Type:        ESTreeKindExportDefaultDeclaration,
			Range:       Range{c.getNodeStart(exportKeyword), r[1]},
			Declaration: result,
			ExportKind:  "value",
		})
	}

	isType := result.GetType() == ESTreeKindTSInterfaceDeclaration || result.GetType() == ESTreeKindTSTypeAliasDeclaration
	res := reflect.Indirect(reflect.ValueOf(result))
	isDeclareField := res.FieldByName("Declare")
	isDeclare := isDeclareField.IsValid() && isDeclareField.Bool()

	exportKind := "value"
	if isType || isDeclare {
		exportKind = "type"
	}

	return c.createNode(node, &ExportNamedDeclaration{
		Type:        ESTreeKindExportNamedDeclaration,
		Range:       Range{c.getNodeStart(exportKeyword), r[1]},
		Attributes:  []*ImportAttribute{},
		Declaration: result,
		ExportKind:  exportKind,
		Specifiers:  []any{},
	})
}

func (c *converter) convertTSTypeParametersToTypeParametersDeclaration(typeParameters *ast.NodeList) *TSTypeParameterDeclaration {
	if typeParameters == nil {
		return nil
	}
	greaterThanTokenRange := scanner.GetRangeOfTokenAtPosition(c.sourceFile, typeParameters.End())
	r := Range{typeParameters.Pos() - 1, greaterThanTokenRange.End()}
	return &TSTypeParameterDeclaration{
		Type:   ESTreeKindTSTypeParameterDeclaration,
		Loc:    c.getLocFor(r),
		Range:  r,
		Params: convertNodeListToChildren[*TSTypeParameter](c, typeParameters),
	}
}

// Source: typescript-go/internal/core/core.go
func Map[T, U any](slice []T, f func(T) U) []U {
	if len(slice) == 0 {
		return nil
	}
	result := make([]U, len(slice))
	for i, value := range slice {
		result[i] = f(value)
	}
	return result
}

// Source: typescript-go/internal/core/core.go
func Filter[T any](slice []T, f func(T) bool) []T {
	for i, value := range slice {
		if !f(value) {
			result := slices.Clone(slice[:i])
			for i++; i < len(slice); i++ {
				value = slice[i]
				if f(value) {
					result = append(result, value)
				}
			}
			return result
		}
	}
	return slice
}

func setProperty(d any, propertyName string, propertyValue any) {
	p := reflect.Indirect(reflect.ValueOf(d))
	p.FieldByName(propertyName).Set(reflect.ValueOf(propertyValue))
}

func (c *converter) convertDecorators(node *ast.Node) []*Decorator {
	decorators := []*Decorator{}
	if modifiers := node.ModifierNodes(); modifiers != nil {
		for _, m := range modifiers {
			if ast.IsDecorator(m) {
				decorators = append(decorators, c.convertChild(m, nil).(*Decorator))
			}
		}
	}
	return decorators
}

func (c *converter) convertParameters(parameters *ast.NodeList) []any {
	if parameters == nil {
		return []any{}
	}

	return Map(parameters.Nodes, func(param *ast.Node) any {
		convertedParam := c.convertChild(param, nil)

		setProperty(convertedParam, "Decorators", c.convertDecorators(param))

		return convertedParam
	})
}

func (c *converter) convertBindingNameWithTypeAnnotation(name *ast.Node, tsType *ast.Node, parent *ast.Node) any {
	id := c.convertPattern(name, nil)

	if tsType != nil {
		typeAnnotation := c.convertTypeAnnotation(tsType, parent)
		setProperty(id, "TypeAnnotation", typeAnnotation)
		c.fixParentLocation(id, typeAnnotation)
	}

	return id
}

func getDeclarationKind(node *ast.VariableDeclarationList) string {
	if node.Flags&ast.NodeFlagsLet != 0 {
		return "let"
	}
	if (node.Flags & ast.NodeFlagsAwaitUsing) == ast.NodeFlagsAwaitUsing {
		return "await using"
	}
	if node.Flags&ast.NodeFlagsConst != 0 {
		return "const"
	}
	if node.Flags&ast.NodeFlagsUsing != 0 {
		return "using"
	}
	return "var"
}

func getTSNodeAccessibility(node *ast.Node) any {
	modifiers := node.ModifierNodes()
	if modifiers == nil {
		return nil
	}
	for _, m := range modifiers {
		switch m.Kind {
		case ast.KindPublicKeyword:
			return "public"
		case ast.KindProtectedKeyword:
			return "protected"
		case ast.KindPrivateKeyword:
			return "private"
		}
	}

	return nil
}

func (c *converter) convertMethodSignature(node *ast.Node) NodeWithRange {
	name := node.Name()
	var kind string
	var optional bool
	switch node.Kind {
	case ast.KindGetAccessor:
		kind = "get"
		optional = node.AsGetAccessorDeclaration().PostfixToken != nil && node.AsGetAccessorDeclaration().PostfixToken.Kind == ast.KindQuestionToken
	case ast.KindSetAccessor:
		kind = "set"
		optional = node.AsSetAccessorDeclaration().PostfixToken != nil && node.AsSetAccessorDeclaration().PostfixToken.Kind == ast.KindQuestionToken
	case ast.KindMethodSignature:
		kind = "method"
		optional = node.AsMethodSignatureDeclaration().PostfixToken != nil && node.AsMethodSignatureDeclaration().PostfixToken.Kind == ast.KindQuestionToken
	}
	return c.createNode(node, &TSMethodSignature{
		Type:           ESTreeKindTSMethodSignature,
		Accessibility:  getTSNodeAccessibility(node),
		Computed:       ast.IsComputedPropertyName(name),
		Key:            c.convertChild(name, nil),
		Kind:           kind,
		Optional:       optional,
		Params:         c.convertParameters(node.ParameterList()),
		Readonly:       hasModifier(ast.KindReadonlyKeyword, node),
		ReturnType:     c.convertTypeAnnotation(node.Type(), node),
		Static:         hasModifier(ast.KindStaticKeyword, node),
		TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(node.TypeParameterList()),
	})
}

func (c *converter) fixParentLocation(result NodeWithRange, child NodeWithRange) {
	r := result.GetRange()
	l := result.GetLoc()
	childRange := child.GetRange()
	childLoc := child.GetLoc()
	if childRange[0] < r[0] {
		r = Range{childRange[0], r[1]}
		result.setRange(r)
		l = SourceLocation{
			Start: childLoc.Start,
			End:   l.End,
		}
		result.setLoc(l)
	}

	if childRange[1] > r[1] {
		r = Range{r[0], childRange[1]}
		result.setRange(r)
		l = SourceLocation{
			Start: l.Start,
			End:   childLoc.End,
		}
		result.setLoc(l)
	}
}

func convertNodeListToChildren[T any](c *converter, list *ast.NodeList) []T {
	if list == nil {
		return []T{}
	}
	return utils.Map(list.Nodes, func(n *ast.Node) T {
		return c.convertChild(n, nil).(T)
	})
}
func convertNodeListToChildrenAllowPattern[T any](c *converter, list *ast.NodeList) []T {
	if list == nil {
		return []T{}
	}
	return utils.Map(list.Nodes, func(n *ast.Node) T {
		return c.convertPattern(n, nil).(T)
	})
}

func isThisInTypeQuery(node *ast.Node) bool {
	if !ast.IsThisIdentifier(node) {
		return false
	}
	for ast.IsQualifiedName(node.Parent) && node.Parent.AsQualifiedName().Left == node {
		node = node.Parent
	}
	return node.Parent.Kind == ast.KindTypeQuery
}

func getLastModifier(node *ast.Node) *ast.Node {
	modifiers := node.ModifierNodes()
	if modifiers == nil || len(modifiers) == 0 {
		return nil
	}
	return modifiers[len(modifiers)-1]
}

func (c *converter) convertTypeArgumentsToTypeParameterInstantiation(typeArguments *ast.NodeList, node *ast.Node) *TSTypeParameterInstantiation {
	if typeArguments == nil {
		return nil
	}
	greaterThanTokenRange := scanner.GetRangeOfTokenAtPosition(c.sourceFile, typeArguments.End())

	return c.createNode(node, &TSTypeParameterInstantiation{
		Type:   ESTreeKindTSTypeParameterInstantiation,
		Range:  Range{typeArguments.Pos() - 1, greaterThanTokenRange.End()},
		Params: convertNodeListToChildren[any](c, typeArguments),
	}).(*TSTypeParameterInstantiation)
}

func canContainDirective(node *ast.Node) bool {
	if node.Kind != ast.KindBlock {
		return true
	}

	switch node.Parent.Kind {
	case ast.KindConstructor,
		ast.KindGetAccessor,
		ast.KindSetAccessor,
		ast.KindArrowFunction,
		ast.KindFunctionExpression,
		ast.KindFunctionDeclaration,
		ast.KindMethodDeclaration:
		return true
	}
	return false
}

func (c *converter) convertBodyExpressions(nodes *ast.NodeList, parent *ast.Node) []any {
	allowDirectives := canContainDirective(parent)

	return Filter(Map(nodes.Nodes, func(statement *ast.Node) any {
		child := c.convertChild(statement, nil)
		if allowDirectives {
			allowDirectives = false

			if child != nil && ast.IsExpressionStatement(statement) {
				s := statement.AsExpressionStatement()
				if ast.IsStringLiteral(s.Expression) {
					setProperty(child, "Directive", s.Expression.AsStringLiteral().Text)
					return child
				}
			}
		}
		return child
	}), func(c any) bool {
		return c != nil
	})
}

func (c *converter) convertJSXIdentifier(node *ast.Node) NodeWithRange {
	result := c.createNode(node, &JSXIdentifier{
		Type: ESTreeKindJSXIdentifier,
		Name: scanner.GetSourceTextOfNodeFromSourceFile(c.sourceFile, node, false),
	})
	c.registerTSNodeInNodeMap(node, result)
	return result
}

func (c *converter) convertJSXNamespaceOrIdentifier(node *ast.Node) NodeWithRange {
	if node.Kind == ast.KindJsxNamespacedName {
		n := node.AsJsxNamespacedName()
		name := n.Name()
		result := c.createNode(node, &JSXNamespacedName{
			Type: ESTreeKindJSXNamespacedName,
			Name: c.createNode(name, &JSXIdentifier{
				Type: ESTreeKindJSXIdentifier,
				Name: name.Text(),
			}).(*JSXIdentifier),
			Namespace: c.createNode(n.Namespace, &JSXIdentifier{
				Type: ESTreeKindJSXIdentifier,
				Name: n.Namespace.Text(),
			}).(*JSXIdentifier),
		})
		c.registerTSNodeInNodeMap(node, result)
		return result
	}

	return c.convertJSXIdentifier(node)
}

func (c *converter) convertJSXTagName(node *ast.JsxTagNameExpression, parent *ast.Node) NodeWithRange {
	var result NodeWithRange

	switch node.Kind {
	case ast.KindPropertyAccessExpression:
		n := node.AsPropertyAccessExpression()
		result = c.createNode(node, &JSXMemberExpression{
			Type:     ESTreeKindJSXMemberExpression,
			Object:   c.convertJSXTagName(n.Expression, parent),
			Property: c.convertJSXIdentifier(n.Name()).(*JSXIdentifier),
		})
	default:
		return c.convertJSXNamespaceOrIdentifier(node)
	}
	c.registerTSNodeInNodeMap(node, result)
	return result
}

func (c *converter) convertChainExpression(node NodeWithRange, tsNode *ast.Node) NodeWithRange {
	var child NodeWithRange
	var isOptional bool

	t := node.GetType()
	if t == ESTreeKindMemberExpression {
		child = node.(*MemberExpression).Object.(NodeWithRange)
		isOptional = node.(*MemberExpression).Optional
	} else if t == ESTreeKindCallExpression {
		child = node.(*CallExpression).Callee.(NodeWithRange)
		isOptional = node.(*CallExpression).Optional
	} else {
		child = node.(*TSNonNullExpression).Expression.(NodeWithRange)
		isOptional = false
	}

	// (x?.y).z is semantically different, and as such .z is no longer optional
	isChildUnwrappable := child.GetType() == ESTreeKindChainExpression && tsNode.Expression().Kind != ast.KindParenthesizedExpression

	if !isChildUnwrappable && !isOptional {
		return node
	}

	if isChildUnwrappable {
		newChild := child.(*ChainExpression).Expression
		if t == ESTreeKindMemberExpression {
			node.(*MemberExpression).Object = newChild
		} else if t == ESTreeKindCallExpression {
			node.(*CallExpression).Callee = newChild
		} else {
			node.(*TSNonNullExpression).Expression = newChild
		}
	}

	return c.createNode(tsNode, &ChainExpression{
		Type:       ESTreeKindChainExpression,
		Expression: node,
	})
}

func (c *converter) convertNode(node *ast.Node, parent *ast.Node) NodeWithRange {
	switch node.Kind {
	case ast.KindSourceFile:
		n := node.AsSourceFile()
		sourceType := "script"
		if n.ExternalModuleIndicator != nil {
			sourceType = "module"
		}
		tokens, comments := c.parseTokens()
		return c.createNode(node, &Program{
			Type:       ESTreeKindProgram,
			Range:      Range{c.getNodeStart(node), node.End()}, // node.endOfFileToken.end],
			Body:       c.convertBodyExpressions(n.Statements, node),
			Comments:   comments,
			Tokens:     tokens,
			SourceType: sourceType,
		})
	case ast.KindBlock:
		n := node.AsBlock()
		return c.createNode(node, &BlockStatement{
			Type: ESTreeKindBlockStatement,
			Body: c.convertBodyExpressions(n.Statements, node),
		})
	case ast.KindIdentifier:
		if isThisInTypeQuery(node) {
			// special case for `typeof this.foo` - TS emits an Identifier for `this`
			// but we want to treat it as a ThisExpression for consistency
			return c.createNode(node, &ThisExpression{
				Type: ESTreeKindThisExpression,
			})
		}
		n := node.AsIdentifier()
		return c.createNode(node, &Identifier{
			Type:       ESTreeKindIdentifier,
			Decorators: []*Decorator{},
			Name:       n.Text,
		})
	case ast.KindPrivateIdentifier:
		{
			n := node.AsPrivateIdentifier()
			return c.createNode(node, &PrivateIdentifier{
				Type: ESTreeKindPrivateIdentifier,
				// typescript includes the `#` in the text
				Name: n.Text[1:],
			})
		}

	case ast.KindWithStatement:
		n := node.AsWithStatement()
		return c.createNode(node, &WithStatement{
			Type:   ESTreeKindWithStatement,
			Body:   c.convertChild(n.Statement, nil),
			Object: c.convertChild(n.Expression, nil),
		})

	// Control Flow

	case ast.KindReturnStatement:
		n := node.AsReturnStatement()
		return c.createNode(node, &ReturnStatement{
			Type:     ESTreeKindReturnStatement,
			Argument: c.convertChild(n.Expression, nil),
		})

	case ast.KindLabeledStatement:
		n := node.AsLabeledStatement()
		return c.createNode(node, &LabeledStatement{
			Type:  ESTreeKindLabeledStatement,
			Body:  c.convertChild(n.Statement, nil),
			Label: convertChildT[*Identifier](c, n.Label, nil),
		})

	case ast.KindContinueStatement:
		n := node.AsContinueStatement()
		return c.createNode(node, &ContinueStatement{
			Type:  ESTreeKindContinueStatement,
			Label: convertChildT[*Identifier](c, n.Label, nil),
		})

	case ast.KindBreakStatement:
		n := node.AsBreakStatement()
		return c.createNode(node, &BreakStatement{
			Type:  ESTreeKindBreakStatement,
			Label: convertChildT[*Identifier](c, n.Label, nil),
		})

	// Choice

	case ast.KindIfStatement:
		n := node.AsIfStatement()
		return c.createNode(node, &IfStatement{
			Type:       ESTreeKindIfStatement,
			Alternate:  c.convertChild(n.ElseStatement, nil),
			Consequent: c.convertChild(n.ThenStatement, nil),
			Test:       c.convertChild(n.Expression, nil),
		})

	case ast.KindSwitchStatement:
		n := node.AsSwitchStatement()
		return c.createNode(node, &SwitchStatement{
			Type:         ESTreeKindSwitchStatement,
			Cases:        convertNodeListToChildren[*SwitchCase](c, n.CaseBlock.AsCaseBlock().Clauses),
			Discriminant: c.convertChild(n.Expression, nil),
		})

	case ast.KindCaseClause, ast.KindDefaultClause:
		var test any
		n := node.AsCaseOrDefaultClause()
		if ast.IsCaseClause(node) {
			test = c.convertChild(n.Expression, nil)
		}

		return c.createNode(node, &SwitchCase{
			Type: ESTreeKindSwitchCase,
			// expression is present in case only
			Consequent: convertNodeListToChildren[any](c, n.Statements),
			Test:       test,
		})

	// Exceptions

	case ast.KindThrowStatement:
		n := node.AsThrowStatement()
		return c.createNode(node, &ThrowStatement{
			Type:     ESTreeKindThrowStatement,
			Argument: c.convertChild(n.Expression, nil),
		})

	case ast.KindTryStatement:
		n := node.AsTryStatement()
		return c.createNode(node, &TryStatement{
			Type:      ESTreeKindTryStatement,
			Block:     convertChildT[*BlockStatement](c, n.TryBlock, nil),
			Finalizer: convertChildT[*BlockStatement](c, n.FinallyBlock, nil),
			Handler:   convertChildT[*CatchClause](c, n.CatchClause, nil),
		})

	case ast.KindCatchClause:
		n := node.AsCatchClause()
		var param any
		if n.VariableDeclaration != nil {
			param = c.convertBindingNameWithTypeAnnotation(n.VariableDeclaration.Name(), n.VariableDeclaration.Type(), nil)
		}
		return c.createNode(node, &CatchClause{
			Type:  ESTreeKindCatchClause,
			Body:  c.convertChild(n.Block, nil).(*BlockStatement),
			Param: param,
		})

	// Loops

	case ast.KindWhileStatement:
		n := node.AsWhileStatement()
		return c.createNode(node, &WhileStatement{
			Type: ESTreeKindWhileStatement,
			Body: c.convertChild(n.Statement, nil),
			Test: c.convertChild(n.Expression, nil),
		})

	/**
	 * Unlike other parsers, TypeScript calls a "DoWhileStatement"
	 * a "DoStatement"
	 */
	case ast.KindDoStatement:
		n := node.AsDoStatement()
		return c.createNode(node, &DoWhileStatement{
			Type: ESTreeKindDoWhileStatement,
			Body: c.convertChild(n.Statement, nil),
			Test: c.convertChild(n.Expression, nil),
		})

	case ast.KindForStatement:
		n := node.AsForStatement()
		return c.createNode(node, &ForStatement{
			Type:   ESTreeKindForStatement,
			Body:   c.convertChild(n.Statement, nil),
			Init:   c.convertChild(n.Initializer, nil),
			Test:   c.convertChild(n.Condition, nil),
			Update: c.convertChild(n.Incrementor, nil),
		})

	case ast.KindForInStatement:
		n := node.AsForInOrOfStatement()
		return c.createNode(node, &ForInStatement{
			Type:  ESTreeKindForInStatement,
			Body:  c.convertChild(n.Statement, nil),
			Left:  c.convertPattern(n.Initializer, nil),
			Right: c.convertChild(n.Expression, nil),
		})

	case ast.KindForOfStatement:
		n := node.AsForInOrOfStatement()
		return c.createNode(node, &ForOfStatement{
			Type:  ESTreeKindForOfStatement,
			Await: n.AwaitModifier != nil && n.AwaitModifier.Kind == ast.KindAwaitKeyword,
			Body:  c.convertChild(n.Statement, nil),
			Left:  c.convertPattern(n.Initializer, nil),
			Right: c.convertChild(n.Expression, nil),
		})

	// Declarations

	case ast.KindFunctionDeclaration:
		n := node.AsFunctionDeclaration()
		isDeclare := hasModifier(ast.KindDeclareKeyword, node)
		isAsync := hasModifier(ast.KindAsyncKeyword, node)
		isGenerator := n.AsteriskToken != nil

		var body any
		if n.Body != nil {
			body = c.convertChild(n.Body, nil)
		}
		var returnType *TSTypeAnnotation
		if n.Type != nil {
			returnType = c.convertTypeAnnotation(n.Type, node)
		}
		var typeParameters *TSTypeParameterDeclaration
		if n.TypeParameters != nil {
			typeParameters = c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters)
		}

		var data NodeWithRange
		if n.Body == nil {
			data = &TSDeclareFunction{
				Type:           ESTreeKindTSDeclareFunction,
				Async:          isAsync,
				Body:           body,
				Declare:        isDeclare,
				Expression:     false,
				Generator:      isGenerator,
				Id:             convertChildT[*Identifier](c, n.Name(), nil),
				Params:         c.convertParameters(n.Parameters),
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			}
		} else {
			data = &FunctionDeclaration{
				Type:           ESTreeKindFunctionDeclaration,
				Async:          isAsync,
				Body:           body.(*BlockStatement),
				Declare:        isDeclare,
				Expression:     false,
				Generator:      isGenerator,
				Id:             convertChildT[*Identifier](c, n.Name(), nil),
				Params:         c.convertParameters(n.Parameters),
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			}
		}

		result := c.createNode(node, data)

		return c.fixExports(node, result)

	case ast.KindVariableDeclaration:
		n := node.AsVariableDeclaration()
		definite := n.ExclamationToken != nil
		init := c.convertChild(n.Initializer, nil)
		id := c.convertBindingNameWithTypeAnnotation(n.Name(), n.Type, node)
		return c.createNode(node, &VariableDeclarator{
			Type:     ESTreeKindVariableDeclarator,
			Definite: definite,
			Id:       id,
			Init:     init,
		})

	case ast.KindVariableStatement:
		n := node.AsVariableStatement()
		declarationList := n.DeclarationList.AsVariableDeclarationList()
		result := c.createNode(node, &VariableDeclaration{
			Type:         ESTreeKindVariableDeclaration,
			Declarations: convertNodeListToChildren[any](c, declarationList.Declarations),
			Declare:      hasModifier(ast.KindDeclareKeyword, node),
			Kind:         getDeclarationKind(declarationList),
		})

		return c.fixExports(node, result)

	// mostly for for-of, for-in
	case ast.KindVariableDeclarationList:
		n := node.AsVariableDeclarationList()
		return c.createNode(node, &VariableDeclaration{
			Type:         ESTreeKindVariableDeclaration,
			Declarations: convertNodeListToChildren[any](c, n.Declarations),
			Declare:      false,
			Kind:         getDeclarationKind(n),
		})

	// Expressions

	case ast.KindExpressionStatement:
		n := node.AsExpressionStatement()
		return c.createNode(node, &ExpressionStatement{
			Type:       ESTreeKindExpressionStatement,
			Directive:  nil,
			Expression: c.convertChild(n.Expression, nil),
		})

	case ast.KindThisKeyword:
		return c.createNode(node, &ThisExpression{
			Type: ESTreeKindThisExpression,
		})

	case ast.KindArrayLiteralExpression:
		n := node.AsArrayLiteralExpression()
		// TypeScript uses ArrayLiteralExpression in destructuring assignment, too
		if c.allowPattern {
			return c.createNode(node, &ArrayPattern{
				Type:       ESTreeKindArrayPattern,
				Decorators: []*Decorator{},
				Elements: Map(n.Elements.Nodes, func(n *ast.Node) any {
					return c.convertPattern(n, nil)
				}),
				Optional:       false,
				TypeAnnotation: nil,
			})
		}
		return c.createNode(node, &ArrayExpression{
			Type: ESTreeKindArrayExpression,
			Elements: Map(n.Elements.Nodes, func(n *ast.Node) any {
				return c.convertChild(n, nil)
			}),
		})

	case ast.KindObjectLiteralExpression:
		n := node.AsObjectLiteralExpression()
		// TypeScript uses ObjectLiteralExpression in destructuring assignment, too
		if c.allowPattern {
			return c.createNode(node, &ObjectPattern{
				Type:           ESTreeKindObjectPattern,
				Decorators:     []*Decorator{},
				Optional:       false,
				Properties:     convertNodeListToChildrenAllowPattern[any](c, n.Properties),
				TypeAnnotation: nil,
			})
		}

		return c.createNode(node, &ObjectExpression{
			Type:       ESTreeKindObjectExpression,
			Properties: convertNodeListToChildren[any](c, n.Properties),
		})

	case ast.KindPropertyAssignment:
		{
			n := node.AsPropertyAssignment()
			name := n.Name()
			return c.createNode(node, &Property{
				Type:      ESTreeKindProperty,
				Computed:  ast.IsComputedPropertyName(name),
				Key:       c.convertChild(name, nil),
				Kind:      "init",
				Method:    false,
				Optional:  false,
				Shorthand: false,
				Value:     c.converter(n.Initializer, node, c.allowPattern),
			})
		}

	case ast.KindShorthandPropertyAssignment:
		n := node.AsShorthandPropertyAssignment()
		name := n.Name()
		if n.ObjectAssignmentInitializer != nil {
			return c.createNode(node, &Property{
				Type:      ESTreeKindProperty,
				Computed:  false,
				Key:       c.convertChild(name, nil),
				Kind:      "init",
				Method:    false,
				Optional:  false,
				Shorthand: true,
				Value: c.createNode(node, &AssignmentPattern{
					Type:           ESTreeKindAssignmentPattern,
					Decorators:     []*Decorator{},
					Left:           c.convertChild(name, nil),
					Optional:       false,
					Right:          c.convertChild(n.ObjectAssignmentInitializer, nil),
					TypeAnnotation: nil,
				}),
			})
		}
		return c.createNode(node, &Property{
			Type:      ESTreeKindProperty,
			Computed:  false,
			Key:       c.convertChild(name, nil),
			Kind:      "init",
			Method:    false,
			Optional:  false,
			Shorthand: true,
			Value:     c.convertChild(name, nil),
		})

	case ast.KindComputedPropertyName:
		n := node.AsComputedPropertyName()
		return c.convertChild(n.Expression, nil)

	case ast.KindPropertyDeclaration:
		n := node.AsPropertyDeclaration()
		name := n.Name()

		isAbstract := hasModifier(ast.KindAbstractKeyword, node)
		isAccessor := hasModifier(ast.KindAccessorKeyword, node)

		accessibility := getTSNodeAccessibility(node)
		computed := ast.IsComputedPropertyName(name)
		declare := hasModifier(ast.KindDeclareKeyword, node)
		decorators := c.convertDecorators(node)
		definite := n.PostfixToken != nil && n.PostfixToken.Kind == ast.KindExclamationToken
		key := c.convertChild(name, nil)
		optional := (key.GetType() == ESTreeKindLiteral ||
			ast.IsIdentifier(name) ||
			ast.IsComputedPropertyName(name) ||
			ast.IsPrivateIdentifier(name)) && n.PostfixToken != nil && n.PostfixToken.Kind == ast.KindQuestionToken
		override := hasModifier(ast.KindOverrideKeyword, node)
		readonly := hasModifier(ast.KindReadonlyKeyword, node)
		static := hasModifier(ast.KindStaticKeyword, node)
		var typeAnnotation *TSTypeAnnotation
		if n.Type != nil {
			typeAnnotation = c.convertTypeAnnotation(n.Type, node)
		}
		var value any
		if !isAbstract {
			value = c.convertChild(n.Initializer, nil)
		}

		var data NodeWithRange
		if isAccessor {
			if isAbstract {
				data = &TSAbstractAccessorProperty{
					Type:           ESTreeKindTSAbstractAccessorProperty,
					Accessibility:  accessibility,
					Computed:       computed,
					Declare:        declare,
					Decorators:     decorators,
					Definite:       definite,
					Key:            key,
					Optional:       optional,
					Override:       override,
					Readonly:       readonly,
					Static:         static,
					TypeAnnotation: typeAnnotation,
					Value:          value,
				}
			} else {
				data = &AccessorProperty{
					Type:           ESTreeKindAccessorProperty,
					Accessibility:  accessibility,
					Computed:       computed,
					Declare:        declare,
					Decorators:     decorators,
					Definite:       definite,
					Key:            key,
					Optional:       optional,
					Override:       override,
					Readonly:       readonly,
					Static:         static,
					TypeAnnotation: typeAnnotation,
					Value:          value,
				}
			}
		} else if isAbstract {
			data = &TSAbstractPropertyDefinition{
				Type:           ESTreeKindTSAbstractPropertyDefinition,
				Accessibility:  accessibility,
				Computed:       computed,
				Declare:        declare,
				Decorators:     decorators,
				Definite:       definite,
				Key:            key,
				Optional:       optional,
				Override:       override,
				Readonly:       readonly,
				Static:         static,
				TypeAnnotation: typeAnnotation,
				Value:          value,
			}
		} else {
			data = &PropertyDefinition{
				Type:           ESTreeKindPropertyDefinition,
				Accessibility:  accessibility,
				Computed:       computed,
				Declare:        declare,
				Decorators:     decorators,
				Definite:       definite,
				Key:            key,
				Optional:       optional,
				Override:       override,
				Readonly:       readonly,
				Static:         static,
				TypeAnnotation: typeAnnotation,
				Value:          value,
			}
		}

		return c.createNode(node, data)
	case ast.KindGetAccessor,
		ast.KindSetAccessor:
		if node.Parent.Kind == ast.KindInterfaceDeclaration ||
			node.Parent.Kind == ast.KindTypeLiteral {
			return c.convertMethodSignature(node)
		}
		// otherwise, it is a non-type accessor
		fallthrough
	case ast.KindMethodDeclaration:
		name := node.Name()

		var postfixToken *ast.Node
		var asteriskToken *ast.Node

		switch node.Kind {
		case ast.KindGetAccessor:
			n := node.AsGetAccessorDeclaration()
			asteriskToken = n.AsteriskToken
			postfixToken = n.PostfixToken
		case ast.KindSetAccessor:
			n := node.AsSetAccessorDeclaration()
			asteriskToken = n.AsteriskToken
			postfixToken = n.PostfixToken
		case ast.KindMethodDeclaration:
			n := node.AsMethodDeclaration()
			asteriskToken = n.AsteriskToken
			postfixToken = n.PostfixToken
		}

		r := Range{node.ParameterList().Pos() - 1, node.End()}
		async := hasModifier(ast.KindAsyncKeyword, node)
		body := c.convertChild(node.Body(), nil)
		declare := false
		expression := false
		generator := asteriskToken != nil
		params := []any{}
		if parent.Kind == ast.KindObjectLiteralExpression {
			params = convertNodeListToChildren[any](c, node.ParameterList())
		} else {
			// Unlike in object literal methods, class method params can have decorators
			params = c.convertParameters(node.ParameterList())
		}
		returnType := c.convertTypeAnnotation(node.Type(), node)
		typeParameters := c.convertTSTypeParametersToTypeParametersDeclaration(node.TypeParameterList())

		var method NodeWithRange
		if body == nil {
			method = c.createNode(node, &TSEmptyBodyFunctionExpression{
				Type:           ESTreeKindTSEmptyBodyFunctionExpression,
				Range:          r,
				Async:          async,
				Body:           body,
				Declare:        declare,
				Expression:     expression,
				Generator:      generator,
				Id:             nil,
				Params:         params,
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			})
		} else {
			method = c.createNode(node, &FunctionExpression{
				Type:           ESTreeKindFunctionExpression,
				Range:          r,
				Async:          async,
				Body:           body.(*BlockStatement),
				Declare:        declare,
				Expression:     expression,
				Generator:      generator,
				Id:             nil,
				Params:         params,
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			})
		}

		if typeParameters != nil {
			c.fixParentLocation(method, typeParameters)
		}

		var kind string
		if parent.Kind == ast.KindObjectLiteralExpression {
			kind = "init"
		} else {
			kind = "method"
		}

		static := hasModifier(ast.KindStaticKeyword, node)

		if node.Kind == ast.KindGetAccessor {
			kind = "get"
		} else if node.Kind == ast.KindSetAccessor {
			kind = "set"
		} else if !static && name.Kind == ast.KindStringLiteral && name.AsStringLiteral().Text == "constructor" {
			kind = "constructor"
		}

		computed := ast.IsComputedPropertyName(name)
		key := c.convertChild(name, nil)
		optional := postfixToken != nil && postfixToken.Kind == ast.KindQuestionToken

		if parent.Kind == ast.KindObjectLiteralExpression {
			return c.createNode(node, &Property{
				Type:      ESTreeKindProperty,
				Computed:  computed,
				Key:       key,
				Kind:      kind,
				Method:    node.Kind == ast.KindMethodDeclaration,
				Optional:  optional,
				Shorthand: false,
				Value:     method,
			})
		}

		accessibility := getTSNodeAccessibility(node)
		decorators := c.convertDecorators(node)
		override := hasModifier(ast.KindOverrideKeyword, node)
		/**
		 * TypeScript class methods can be defined as "abstract"
		 */
		if hasModifier(ast.KindAbstractKeyword, node) {
			return c.createNode(node, &TSAbstractMethodDefinition{
				Type:          ESTreeKindTSAbstractMethodDefinition,
				Accessibility: accessibility,
				Computed:      computed,
				Decorators:    decorators,
				Key:           key,
				Kind:          kind,
				Optional:      optional,
				Override:      override,
				Static:        static,
				Value:         method,
			})
		}
		return c.createNode(node, &MethodDefinition{
			Type:          ESTreeKindMethodDefinition,
			Accessibility: accessibility,
			Computed:      computed,
			Decorators:    decorators,
			Key:           key,
			Kind:          kind,
			Optional:      optional,
			Override:      override,
			Static:        static,
			Value:         method,
		})
	// TypeScript uses this even for static methods named "constructor"
	case ast.KindConstructor:
		n := node.AsConstructorDeclaration()

		lastModifier := getLastModifier(node)
		var constructorTokenRange core.TextRange
		if lastModifier != nil {
			constructorTokenRange = scanner.GetRangeOfTokenAtPosition(c.sourceFile, lastModifier.End())
		}
		if constructorTokenRange.End() == 0 {
			constructorTokenRange = scanner.GetRangeOfTokenAtPosition(c.sourceFile, node.Pos())
		}

		r := Range{n.Parameters.Pos() - 1, node.End()}
		async := false
		body := c.convertChild(n.Body, nil)
		declare := false
		expression := false
		generator := false
		params := c.convertParameters(n.Parameters)
		returnType := c.convertTypeAnnotation(n.Type, node)
		typeParameters := c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters)

		var constructor NodeWithRange
		if n.Body == nil {
			constructor = c.createNode(node, &TSEmptyBodyFunctionExpression{
				Type:           ESTreeKindTSEmptyBodyFunctionExpression,
				Range:          r,
				Async:          async,
				Body:           body,
				Declare:        declare,
				Expression:     expression,
				Generator:      generator,
				Id:             nil,
				Params:         params,
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			})
		} else {
			constructor = c.createNode(node, &FunctionExpression{
				Type:           ESTreeKindFunctionExpression,
				Range:          r,
				Async:          async,
				Body:           body.(*BlockStatement),
				Declare:        declare,
				Expression:     expression,
				Generator:      generator,
				Id:             nil,
				Params:         params,
				ReturnType:     returnType,
				TypeParameters: typeParameters,
			})
		}

		if typeParameters != nil {
			c.fixParentLocation(constructor, typeParameters)
		}

		constructorKey := c.createNode(node, &Identifier{
			Type:           ESTreeKindIdentifier,
			Range:          Range{constructorTokenRange.Pos(), constructorTokenRange.End()},
			Decorators:     []*Decorator{},
			Name:           "constructor",
			Optional:       false,
			TypeAnnotation: nil,
		})

		isStatic := hasModifier(ast.KindStaticKeyword, node)
		accessibility := getTSNodeAccessibility(node)
		computed := false
		decorators := []*Decorator{}
		kind := "constructor"
		if isStatic {
			kind = "method"
		}
		optional := false
		override := false

		if hasModifier(ast.KindAbstractKeyword, node) {
			return c.createNode(node, &TSAbstractMethodDefinition{
				Type:          ESTreeKindTSAbstractMethodDefinition,
				Accessibility: accessibility,
				Computed:      computed,
				Decorators:    decorators,
				Key:           constructorKey,
				Kind:          kind,
				Optional:      optional,
				Override:      override,
				Static:        isStatic,
				Value:         constructor,
			})
		}
		return c.createNode(node, &MethodDefinition{
			Type:          ESTreeKindMethodDefinition,
			Accessibility: accessibility,
			Computed:      computed,
			Decorators:    decorators,
			Key:           constructorKey,
			Kind:          kind,
			Optional:      optional,
			Override:      override,
			Static:        isStatic,
			Value:         constructor,
		})

	case ast.KindFunctionExpression:
		n := node.AsFunctionExpression()
		return c.createNode(node, &FunctionExpression{
			Type:           ESTreeKindFunctionExpression,
			Async:          hasModifier(ast.KindAsyncKeyword, node),
			Body:           c.convertChild(n.Body, nil).(*BlockStatement),
			Declare:        false,
			Expression:     false,
			Generator:      n.AsteriskToken != nil,
			Id:             convertChildT[*Identifier](c, n.Name(), nil),
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

	case ast.KindSuperKeyword:
		return c.createNode(node, &Super{
			Type: ESTreeKindSuper,
		})

	case ast.KindArrayBindingPattern:
		n := node.AsBindingPattern()
		return c.createNode(node, &ArrayPattern{
			Type:       ESTreeKindArrayPattern,
			Decorators: []*Decorator{},
			Elements: Map(n.Elements.Nodes, func(n *ast.Node) any {
				return c.convertPattern(n, nil)
			}),
			Optional:       false,
			TypeAnnotation: nil,
		})

	// occurs with missing array elements like [,]
	case ast.KindOmittedExpression:
		return nil

	case ast.KindObjectBindingPattern:
		n := node.AsBindingPattern()
		return c.createNode(node, &ObjectPattern{
			Type:           ESTreeKindObjectPattern,
			Decorators:     []*Decorator{},
			Optional:       false,
			Properties:     convertNodeListToChildrenAllowPattern[any](c, n.Elements),
			TypeAnnotation: nil,
		})

	case ast.KindBindingElement:
		n := node.AsBindingElement()
		name := n.Name()
		if parent.Kind == ast.KindArrayBindingPattern {
			arrayItem := c.convertChild(name, parent)

			if n.Initializer != nil {
				return c.createNode(node, &AssignmentPattern{
					Type:           ESTreeKindAssignmentPattern,
					Decorators:     []*Decorator{},
					Left:           arrayItem,
					Optional:       false,
					Right:          c.convertChild(n.Initializer, nil),
					TypeAnnotation: nil,
				})
			}

			if n.DotDotDotToken != nil {
				return c.createNode(node, &RestElement{
					Type:           ESTreeKindRestElement,
					Argument:       arrayItem,
					Decorators:     []*Decorator{},
					Optional:       false,
					TypeAnnotation: nil,
					Value:          nil,
				})
			}
			return arrayItem
		}

		var value NodeWithRange
		if n.Initializer != nil {
			value = c.createNode(node, &AssignmentPattern{
				Type:           ESTreeKindAssignmentPattern,
				Range:          Range{c.getNodeStart(name), n.Initializer.End()},
				Decorators:     []*Decorator{},
				Left:           c.convertChild(name, nil),
				Optional:       false,
				Right:          c.convertChild(n.Initializer, nil),
				TypeAnnotation: nil,
			})
		}

		argument := n.PropertyName
		if argument == nil {
			argument = name
		}

		if n.DotDotDotToken != nil {
			return c.createNode(node, &RestElement{
				Type:           ESTreeKindRestElement,
				Argument:       c.convertChild(argument, nil),
				Decorators:     []*Decorator{},
				Optional:       false,
				TypeAnnotation: nil,
				Value:          nil,
			})
		}

		if value == nil {
			value = c.convertChild(name, nil)
		}
		return c.createNode(node, &Property{
			Type:      ESTreeKindProperty,
			Computed:  n.PropertyName != nil && n.PropertyName.Kind == ast.KindComputedPropertyName,
			Key:       c.convertChild(argument, nil),
			Kind:      "init",
			Method:    false,
			Optional:  false,
			Shorthand: n.PropertyName == nil,
			Value:     value,
		})

	case ast.KindArrowFunction:
		n := node.AsArrowFunction()
		return c.createNode(node, &ArrowFunctionExpression{
			Type:           ESTreeKindArrowFunctionExpression,
			Async:          hasModifier(ast.KindAsyncKeyword, node),
			Body:           c.convertChild(n.Body, nil),
			Expression:     n.Body.Kind != ast.KindBlock,
			Generator:      false,
			Id:             nil,
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

	case ast.KindYieldExpression:
		n := node.AsYieldExpression()
		return c.createNode(node, &YieldExpression{
			Type:     ESTreeKindYieldExpression,
			Argument: c.convertChild(n.Expression, nil),
			Delegate: n.AsteriskToken != nil,
		})

	case ast.KindAwaitExpression:
		n := node.AsAwaitExpression()
		return c.createNode(node, &AwaitExpression{
			Type:     ESTreeKindAwaitExpression,
			Argument: c.convertChild(n.Expression, nil),
		})

	// Template Literals

	case ast.KindNoSubstitutionTemplateLiteral:
		n := node.AsNoSubstitutionTemplateLiteral()
		return c.createNode(node, &TemplateLiteral{
			Type:        ESTreeKindTemplateLiteral,
			Expressions: []any{},
			Quasis: []*TemplateElement{
				c.createNode(node, &TemplateElement{
					Type: ESTreeKindTemplateElement,
					Tail: true,
					Value: struct {
						Cooked string
						Raw    string
					}{
						Cooked: n.Text,
						Raw:    c.sourceFile.Text[c.getNodeStart(node)+1 : node.End()-1],
					},
				}).(*TemplateElement),
			},
		})

	case ast.KindTemplateExpression:
		n := node.AsTemplateExpression()
		expressions := []any{}
		quasis := []*TemplateElement{c.convertChild(n.Head, nil).(*TemplateElement)}

		for _, templateSpan := range n.TemplateSpans.Nodes {
			s := templateSpan.AsTemplateSpan()
			expressions = append(expressions, c.convertChild(s.Expression, nil))
			quasis = append(quasis, c.convertChild(s.Literal, nil).(*TemplateElement))
		}

		return c.createNode(node, &TemplateLiteral{
			Type:        ESTreeKindTemplateLiteral,
			Expressions: expressions,
			Quasis:      quasis,
		})

	case ast.KindTaggedTemplateExpression:
		n := node.AsTaggedTemplateExpression()
		return c.createNode(node, &TaggedTemplateExpression{
			Type:          ESTreeKindTaggedTemplateExpression,
			Quasi:         c.convertChild(n.Template, nil).(*TemplateLiteral),
			Tag:           c.convertChild(n.Tag, nil),
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

	case ast.KindTemplateHead,
		ast.KindTemplateMiddle,
		ast.KindTemplateTail:
		tail := node.Kind == ast.KindTemplateTail

		rawEndOffset := 2
		if tail {
			rawEndOffset = 1
		}

		return c.createNode(node, &TemplateElement{
			Type: ESTreeKindTemplateElement,
			Tail: tail,
			Value: struct {
				Cooked string
				Raw    string
			}{
				Cooked: node.Text(),
				Raw:    c.sourceFile.Text[c.getNodeStart(node)+1 : node.End()-rawEndOffset],
			},
		})

	// Patterns

	case ast.KindSpreadAssignment,
		ast.KindSpreadElement:
		if c.allowPattern {
			return c.createNode(node, &RestElement{
				Type:           ESTreeKindRestElement,
				Argument:       c.convertPattern(node.Expression(), nil),
				Decorators:     []*Decorator{},
				Optional:       false,
				TypeAnnotation: nil,
				Value:          nil,
			})
		}
		return c.createNode(node, &SpreadElement{
			Type:     ESTreeKindSpreadElement,
			Argument: c.convertChild(node.Expression(), nil),
		})

	case ast.KindParameter:
		n := node.AsParameterDeclaration()
		name := node.Name()

		var parameter NodeWithRange
		var result NodeWithRange
		if n.DotDotDotToken != nil {
			parameter = c.createNode(node, &RestElement{
				Type:           ESTreeKindRestElement,
				Argument:       c.convertChild(name, nil),
				Decorators:     []*Decorator{},
				Optional:       false,
				TypeAnnotation: nil,
				Value:          nil,
			})
			result = parameter
		} else if n.Initializer != nil {
			parameter = c.convertChild(name, nil)
			result = c.createNode(node, &AssignmentPattern{
				Type:           ESTreeKindAssignmentPattern,
				Decorators:     []*Decorator{},
				Left:           parameter,
				Optional:       false,
				Right:          c.convertChild(n.Initializer, nil),
				TypeAnnotation: nil,
			})

			if modifiers := node.ModifierNodes(); modifiers != nil {
				// AssignmentPattern should not contain modifiers in range
				r := Range{parameter.GetRange()[0], result.GetRange()[1]}
				result.setRange(r)
				result.setLoc(c.getLocFor(r))
			}
		} else {
			parameter = c.convertChild(name, parent)
			result = parameter
		}

		typeAnnotation := c.convertTypeAnnotation(n.Type, node)
		if typeAnnotation != nil {
			setProperty(parameter, "TypeAnnotation", typeAnnotation)
			c.fixParentLocation(parameter, typeAnnotation)
		}

		if n.QuestionToken != nil {
			if n.QuestionToken.End() > parameter.GetRange()[1] {
				r := Range{parameter.GetRange()[0], n.QuestionToken.End()}
				parameter.setRange(r)
				parameter.setLoc(SourceLocation{
					Start: parameter.GetLoc().Start,
					End:   getLineAndCharacterFor(r[1], c.sourceFile),
				})
			}
			setProperty(parameter, "Optional", true)
		}

		if modifiers := getModifiers(node); modifiers != nil {
			return c.createNode(node, &TSParameterProperty{
				Type:          ESTreeKindTSParameterProperty,
				Accessibility: getTSNodeAccessibility(node),
				Decorators:    []*Decorator{},
				Override:      hasModifier(ast.KindOverrideKeyword, node),
				Parameter:     result,
				Readonly:      hasModifier(ast.KindReadonlyKeyword, node),
				Static:        hasModifier(ast.KindStaticKeyword, node),
			})
		}
		return result

	// Classes

	case ast.KindClassDeclaration,
		ast.KindClassExpression:
		var heritageClauses *ast.NodeList
		if ast.IsClassDeclaration(node) {
			heritageClauses = node.AsClassDeclaration().HeritageClauses
		} else {
			heritageClauses = node.AsClassExpression().HeritageClauses
		}

		var extendsClause *ast.HeritageClause
		var implementsClause *ast.HeritageClause

		if heritageClauses != nil {
			for _, heritageClause := range heritageClauses.Nodes {
				c := heritageClause.AsHeritageClause()
				if c.Token == ast.KindExtendsKeyword && extendsClause == nil {
					extendsClause = c
				} else if c.Token == ast.KindImplementsKeyword && implementsClause == nil {
					implementsClause = c
				}
			}
		}

		memberList := node.MemberList()
		name := node.Name()

		abstract := hasModifier(ast.KindAbstractKeyword, node)
		body := c.createNode(node, &ClassBody{
			Type:  ESTreeKindClassBody,
			Range: Range{memberList.Pos() - 1, node.End()},
			Body: Map(Filter(memberList.Nodes, func(m *ast.Node) bool {
				return m.Kind != ast.KindSemicolonClassElement
			}), func(m *ast.Node) any {
				return c.convertChild(m, nil)
			}),
		}).(*ClassBody)
		declare := hasModifier(ast.KindDeclareKeyword, node)
		decorators := c.convertDecorators(node)
		id, _ := c.convertChild(name, nil).(*Identifier)
		implements := []*TSClassImplements{}
		if implementsClause != nil {
			implements = convertNodeListToChildren[*TSClassImplements](c, implementsClause.Types)
		}
		var superClass any
		var superTypeArguments *TSTypeParameterInstantiation
		if extendsClause != nil && len(extendsClause.Types.Nodes) != 0 {
			super := extendsClause.Types.Nodes[0]
			superClass = c.convertChild(super.Expression(), nil)
			superTypeArguments = c.convertTypeArgumentsToTypeParameterInstantiation(super.TypeArgumentList(), super)
		}
		typeParameters := c.convertTSTypeParametersToTypeParametersDeclaration(node.TypeParameterList())

		var result NodeWithRange
		if ast.IsClassDeclaration(node) {
			result = c.createNode(node, &ClassDeclaration{
				Type:               ESTreeKindClassDeclaration,
				Abstract:           abstract,
				Body:               body,
				Declare:            declare,
				Decorators:         decorators,
				Id:                 id,
				Implements:         implements,
				SuperClass:         superClass,
				SuperTypeArguments: superTypeArguments,
				TypeParameters:     typeParameters,
			})
		} else {
			result = c.createNode(node, &ClassExpression{
				Type:               ESTreeKindClassExpression,
				Abstract:           abstract,
				Body:               body,
				Declare:            declare,
				Decorators:         decorators,
				Id:                 id,
				Implements:         implements,
				SuperClass:         superClass,
				SuperTypeArguments: superTypeArguments,
				TypeParameters:     typeParameters,
			})
		}

		return c.fixExports(node, result)

	// Modules

	case ast.KindModuleBlock:
		n := node.AsModuleBlock()
		return c.createNode(node, &TSModuleBlock{
			Type: ESTreeKindTSModuleBlock,
			Body: c.convertBodyExpressions(n.Statements, node),
		})

	case ast.KindImportDeclaration:
		n := node.AsImportDeclaration()
		attributes := []*ImportAttribute{}
		if n.Attributes != nil {
			attributes = convertNodeListToChildren[*ImportAttribute](c, n.Attributes.AsImportAttributes().Attributes)
		}

		importKind := "value"
		specifiers := []any{}

		if n.ImportClause != nil {
			importClause := n.ImportClause.AsImportClause()
			if importClause.IsTypeOnly {
				importKind = "type"
			}

			if importClause.Name() != nil {
				specifiers = append(specifiers, c.convertChild(n.ImportClause, nil))
			}

			if importClause.NamedBindings != nil {
				switch importClause.NamedBindings.Kind {
				case ast.KindNamespaceImport:
					specifiers = append(specifiers, c.convertChild(importClause.NamedBindings, nil))
				case ast.KindNamedImports:
					specifiers = append(specifiers, convertNodeListToChildren[any](c, importClause.NamedBindings.AsNamedImports().Elements)...)
				}
			}
		}

		result := c.createNode(node, &ImportDeclaration{
			Type:       ESTreeKindImportDeclaration,
			Attributes: attributes,
			ImportKind: importKind,
			Source:     c.convertChild(n.ModuleSpecifier, nil).(*Literal),
			Specifiers: specifiers,
		})
		return result

	case ast.KindNamespaceImport:
		return c.createNode(node, &ImportNamespaceSpecifier{
			Type:  ESTreeKindImportNamespaceSpecifier,
			Local: c.convertChild(node.Name(), nil).(*Identifier),
		})

	case ast.KindImportSpecifier:
		n := node.AsImportSpecifier()
		name := node.Name()
		imported := n.PropertyName
		if imported == nil {
			imported = name
		}
		importKind := "value"
		if n.IsTypeOnly {
			importKind = "type"
		}
		return c.createNode(node, &ImportSpecifier{
			Type:       ESTreeKindImportSpecifier,
			Imported:   c.convertChild(imported, nil),
			ImportKind: importKind,
			Local:      c.convertChild(name, nil).(*Identifier),
		})

	case ast.KindImportClause:
		local := c.convertChild(node.Name(), nil)
		return c.createNode(node, &ImportDefaultSpecifier{
			Type:  ESTreeKindImportDefaultSpecifier,
			Range: local.GetRange(),
			Local: local.(*Identifier),
		})

	case ast.KindExportDeclaration:
		n := node.AsExportDeclaration()

		attributes := []*ImportAttribute{}
		if n.Attributes != nil {
			attributes = convertNodeListToChildren[*ImportAttribute](c, n.Attributes.AsImportAttributes().Attributes)
		}

		exportKind := "value"
		if n.IsTypeOnly {
			exportKind = "type"
		}

		if n.ExportClause != nil && n.ExportClause.Kind == ast.KindNamedExports {
			return c.createNode(node, &ExportNamedDeclaration{
				Type:        ESTreeKindExportNamedDeclaration,
				Attributes:  attributes,
				Declaration: nil,
				ExportKind:  exportKind,
				Source:      convertChildT[*Literal](c, n.ModuleSpecifier, nil),
				Specifiers:  convertNodeListToChildren[any](c, n.ExportClause.AsNamedExports().Elements),
			})
		}

		var exported any
		if n.ExportClause != nil && n.ExportClause.Kind == ast.KindNamespaceExport {
			exported = convertChildT[any](c, n.ExportClause.AsNamespaceExport().Name(), nil)
		}
		return c.createNode(node, &ExportAllDeclaration{
			Type:       ESTreeKindExportAllDeclaration,
			Attributes: attributes,
			Exported:   exported,
			ExportKind: exportKind,
			Source:     convertChildT[*Literal](c, n.ModuleSpecifier, nil),
		})

	case ast.KindExportSpecifier:
		n := node.AsExportSpecifier()
		local := n.PropertyName
		if local == nil {
			local = n.Name()
		}
		exportKind := "value"
		if n.IsTypeOnly {
			exportKind = "type"
		}

		return c.createNode(node, &ExportSpecifier{
			Type:       ESTreeKindExportSpecifier,
			Exported:   c.convertChild(n.Name(), nil),
			ExportKind: exportKind,
			Local:      c.convertChild(local, nil),
		})

	case ast.KindExportAssignment:
		n := node.AsExportAssignment()
		if n.IsExportEquals {
			return c.createNode(node, &TSExportAssignment{
				Type:       ESTreeKindTSExportAssignment,
				Expression: c.convertChild(n.Expression, nil),
			})
		}
		return c.createNode(node, &ExportDefaultDeclaration{
			Type:        ESTreeKindExportDefaultDeclaration,
			Declaration: c.convertChild(n.Expression, nil),
			ExportKind:  "value",
		})

	// Unary Operations

	case ast.KindPrefixUnaryExpression,
		ast.KindPostfixUnaryExpression:
		var operatorToken ast.Kind
		var operand *ast.Node
		if ast.IsPrefixUnaryExpression(node) {
			n := node.AsPrefixUnaryExpression()
			operatorToken = n.Operator
			operand = n.Operand
		} else {
			n := node.AsPostfixUnaryExpression()
			operatorToken = n.Operator
			operand = n.Operand
		}
		operator := scanner.TokenToString(operatorToken)

		if operatorToken == ast.KindPlusPlusToken || operatorToken == ast.KindMinusMinusToken {
			return c.createNode(node, &UpdateExpression{
				Type:     ESTreeKindUpdateExpression,
				Argument: c.convertChild(operand, nil),
				Operator: operator,
				Prefix:   node.Kind == ast.KindPrefixUnaryExpression,
			})
		}
		return c.createNode(node, &UnaryExpression{
			Type:     ESTreeKindUnaryExpression,
			Argument: c.convertChild(operand, nil),
			Operator: operator,
			Prefix:   node.Kind == ast.KindPrefixUnaryExpression,
		})

	case ast.KindDeleteExpression:
		return c.createNode(node, &UnaryExpression{
			Type:     ESTreeKindUnaryExpression,
			Argument: c.convertChild(node.AsDeleteExpression().Expression, nil),
			Operator: "delete",
			Prefix:   true,
		})

	case ast.KindVoidExpression:
		return c.createNode(node, &UnaryExpression{
			Type:     ESTreeKindUnaryExpression,
			Argument: c.convertChild(node.AsVoidExpression().Expression, nil),
			Operator: "void",
			Prefix:   true,
		})

	case ast.KindTypeOfExpression:
		return c.createNode(node, &UnaryExpression{
			Type:     ESTreeKindUnaryExpression,
			Argument: c.convertChild(node.AsTypeOfExpression().Expression, nil),
			Operator: "typeof",
			Prefix:   true,
		})

	case ast.KindTypeOperator:
		n := node.AsTypeOperatorNode()
		return c.createNode(node, &TSTypeOperator{
			Type:           ESTreeKindTSTypeOperator,
			Operator:       scanner.TokenToString(n.Operator),
			TypeAnnotation: c.convertChild(n.Type, nil),
		})

	// Binary Operations

	case ast.KindBinaryExpression:
		n := node.AsBinaryExpression()
		// TypeScript uses BinaryExpression for sequences as well
		if n.OperatorToken.Kind == ast.KindCommaToken {
			expressions := []any{}

			left := c.convertChild(n.Left, nil)

			if left.GetType() == ESTreeKindSequenceExpression && n.Left.Kind != ast.KindParenthesizedExpression {
				expressions = append(expressions, left.(*SequenceExpression).Expressions...)
			} else {
				expressions = append(expressions, left)
			}

			expressions = append(expressions, c.convertChild(n.Right, nil))

			return c.createNode(node, &SequenceExpression{
				Type:        ESTreeKindSequenceExpression,
				Expressions: expressions,
			})
		}

		if ast.IsAssignmentOperator(n.OperatorToken.Kind) {
			if c.allowPattern {
				return c.createNode(node, &AssignmentPattern{
					Type:           ESTreeKindAssignmentPattern,
					Decorators:     []*Decorator{},
					Left:           c.convertPattern(n.Left, node),
					Optional:       false,
					Right:          c.convertChild(n.Right, nil),
					TypeAnnotation: nil,
				})
			}
			return c.createNode(node, &AssignmentExpression{
				Type:     ESTreeKindAssignmentExpression,
				Operator: scanner.TokenToString(n.OperatorToken.Kind),
				Left:     c.convertPattern(n.Left, node),
				Right:    c.convertChild(n.Right, nil),
			})
		}

		if ast.IsLogicalOrCoalescingBinaryOperator(n.OperatorToken.Kind) {
			return c.createNode(node, &LogicalExpression{
				Type:     ESTreeKindLogicalExpression,
				Operator: scanner.TokenToString(n.OperatorToken.Kind),
				Left:     c.convertChild(n.Left, node),
				Right:    c.convertChild(n.Right, nil),
			})
		}

		return c.createNode(node, &BinaryExpression{
			Type:     ESTreeKindBinaryExpression,
			Operator: scanner.TokenToString(n.OperatorToken.Kind),
			Left:     c.convertChild(n.Left, node),
			Right:    c.convertChild(n.Right, nil),
		})

	case ast.KindPropertyAccessExpression:
		n := node.AsPropertyAccessExpression()

		result := c.createNode(node, &MemberExpression{
			Type:     ESTreeKindMemberExpression,
			Computed: false,
			Object:   c.convertChild(n.Expression, nil),
			Optional: n.QuestionDotToken != nil,
			Property: c.convertChild(n.Name(), nil),
		})

		return c.convertChainExpression(result, node)

	case ast.KindElementAccessExpression:
		n := node.AsElementAccessExpression()

		result := c.createNode(node, &MemberExpression{
			Type:     ESTreeKindMemberExpression,
			Computed: true,
			Object:   c.convertChild(n.Expression, nil),
			Optional: n.QuestionDotToken != nil,
			Property: c.convertChild(n.ArgumentExpression, nil),
		})

		return c.convertChainExpression(result, node)

	case ast.KindCallExpression:
		n := node.AsCallExpression()
		if n.Expression.Kind == ast.KindImportKeyword {
			var options any
			var source any
			if len(n.Arguments.Nodes) >= 2 {
				options = c.convertChild(n.Arguments.Nodes[1], nil)
				source = c.convertChild(n.Arguments.Nodes[0], nil)
			} else if len(n.Arguments.Nodes) >= 1 {
				source = c.convertChild(n.Arguments.Nodes[0], nil)
			}

			return c.createNode(node, &ImportExpression{
				Type:    ESTreeKindImportExpression,
				Options: options,
				Source:  source,
			})
		}

		result := c.createNode(node, &CallExpression{
			Type:          ESTreeKindCallExpression,
			Arguments:     convertNodeListToChildren[any](c, n.Arguments),
			Callee:        c.convertChild(n.Expression, nil),
			Optional:      n.QuestionDotToken != nil,
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

		return c.convertChainExpression(result, node)

	case ast.KindNewExpression:
		n := node.AsNewExpression()
		// NOTE - NewExpression cannot have an optional chain in it
		return c.createNode(node, &NewExpression{
			Type:          ESTreeKindNewExpression,
			Arguments:     convertNodeListToChildren[any](c, n.Arguments),
			Callee:        c.convertChild(n.Expression, nil),
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

	case ast.KindConditionalExpression:
		n := node.AsConditionalExpression()
		return c.createNode(node, &ConditionalExpression{
			Type:       ESTreeKindConditionalExpression,
			Alternate:  c.convertChild(n.WhenFalse, nil),
			Consequent: c.convertChild(n.WhenTrue, nil),
			Test:       c.convertChild(n.Condition, nil),
		})

	case ast.KindMetaProperty:
		n := node.AsMetaProperty()
		metaRange := scanner.GetRangeOfTokenAtPosition(c.sourceFile, node.Pos())
		metaR := Range{metaRange.Pos(), metaRange.End()}
		return c.createNode(node, &MetaProperty{
			Type: ESTreeKindMetaProperty,
			Meta: c.createNode(node, &Identifier{
				Type:           ESTreeKindIdentifier,
				Decorators:     []*Decorator{},
				Name:           scanner.TokenToString(n.KeywordToken),
				Optional:       false,
				TypeAnnotation: nil,
				Range:          metaR,
				Loc:            c.getLocFor(metaR),
			}).(*Identifier),
			Property: c.convertChild(n.Name(), nil).(*Identifier),
		})

	case ast.KindDecorator:
		return c.createNode(node, &Decorator{
			Type:       ESTreeKindDecorator,
			Expression: c.convertChild(node.AsDecorator().Expression, nil),
		})

	// Literals

	case ast.KindStringLiteral:
		return c.createNode(node, &Literal{
			Type:  ESTreeKindLiteral,
			Raw:   scanner.GetSourceTextOfNodeFromSourceFile(c.sourceFile, node, false),
			Value: node.AsStringLiteral().Text,
			// TODO(port)
			// parent.kind === ast.KindJsxAttribute
			//   ? unescapeStringLiteralText(node.text)
			//   : node.text,
		})

	case ast.KindNumericLiteral:
		raw := scanner.GetSourceTextOfNodeFromSourceFile(c.sourceFile, node, false)
		v, _ := gojaParser.ParseNumberLiteral(raw)
		return c.createNode(node, &Literal{
			Type:  ESTreeKindLiteral,
			Raw:   raw,
			Value: v,
		})

	case ast.KindBigIntLiteral:
		raw := node.AsBigIntLiteral().Text
		v, _ := gojaParser.ParseNumberLiteral(raw)
		return c.createNode(node, &BigIntLiteral{
			Type:   ESTreeKindLiteral,
			Raw:    raw,
			Value:  v,
			Bigint: v.(*big.Int).String(),
		})

	case ast.KindRegularExpressionLiteral:
		n := node.AsRegularExpressionLiteral()
		terminatorIndex := strings.LastIndex(n.Text, "/")
		pattern := n.Text[1:terminatorIndex]
		flags := n.Text[terminatorIndex+1:]
		raw := n.Text
		return c.createNode(node, &RegExpLiteral{
			Type:  ESTreeKindLiteral,
			Raw:   raw,
			Value: nil, //c.vm.NewRegExp(c.vm.ToValue(pattern), c.vm.ToValue(flags)),
			Regex: struct {
				Pattern string
				Flags   string
			}{
				Pattern: pattern,
				Flags:   flags,
			},
		})

	case ast.KindTrueKeyword:
		return c.createNode(node, &Literal{
			Type:  ESTreeKindLiteral,
			Raw:   "true",
			Value: true,
		})

	case ast.KindFalseKeyword:
		return c.createNode(node, &Literal{
			Type:  ESTreeKindLiteral,
			Raw:   "false",
			Value: false,
		})

	case ast.KindNullKeyword:
		return c.createNode(node, &Literal{
			Type:  ESTreeKindLiteral,
			Raw:   "null",
			Value: nil,
		})

	case ast.KindEmptyStatement:
		return c.createNode(node, &EmptyStatement{
			Type: ESTreeKindEmptyStatement,
		})

	case ast.KindDebuggerStatement:
		return c.createNode(node, &DebuggerStatement{
			Type: ESTreeKindDebuggerStatement,
		})

	// JSX

	case ast.KindJsxElement:
		n := node.AsJsxElement()
		return c.createNode(node, &JSXElement{
			Type:           ESTreeKindJSXElement,
			Children:       convertNodeListToChildren[any](c, n.Children),
			ClosingElement: c.convertChild(n.ClosingElement, nil).(*JSXClosingElement),
			OpeningElement: c.convertChild(n.OpeningElement, nil).(*JSXOpeningElement),
		})

	case ast.KindJsxFragment:
		n := node.AsJsxFragment()
		return c.createNode(node, &JSXFragment{
			Type:            ESTreeKindJSXFragment,
			Children:        convertNodeListToChildren[any](c, n.Children),
			ClosingFragment: c.convertChild(n.ClosingFragment, nil).(*JSXClosingFragment),
			OpeningFragment: c.convertChild(n.OpeningFragment, nil).(*JSXOpeningFragment),
		})

	case ast.KindJsxSelfClosingElement:
		n := node.AsJsxSelfClosingElement()
		return c.createNode(node, &JSXElement{
			Type: ESTreeKindJSXElement,
			/**
			 * Convert ast.KindJsxSelfClosingElement to ast.KindJsxOpeningElement,
			 * TypeScript does not seem to have the idea of openingElement when tag is self-closing
			 */
			Children:       []any{},
			ClosingElement: nil,
			OpeningElement: c.createNode(node, &JSXOpeningElement{
				Type:          ESTreeKindJSXOpeningElement,
				Attributes:    convertNodeListToChildren[any](c, n.Attributes.AsJsxAttributes().Properties),
				Name:          c.convertJSXTagName(n.TagName, node),
				SelfClosing:   true,
				TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
			}).(*JSXOpeningElement),
		})

	case ast.KindJsxOpeningElement:
		n := node.AsJsxOpeningElement()
		return c.createNode(node, &JSXOpeningElement{
			Type:          ESTreeKindJSXOpeningElement,
			Attributes:    convertNodeListToChildren[any](c, n.Attributes.AsJsxAttributes().Properties),
			Name:          c.convertJSXTagName(n.TagName, node),
			SelfClosing:   false,
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

	case ast.KindJsxClosingElement:
		n := node.AsJsxClosingElement()
		return c.createNode(node, &JSXClosingElement{
			Type: ESTreeKindJSXClosingElement,
			Name: c.convertJSXTagName(n.TagName, node),
		})

	case ast.KindJsxOpeningFragment:
		return c.createNode(node, &JSXOpeningFragment{
			Type: ESTreeKindJSXOpeningFragment,
		})

	case ast.KindJsxClosingFragment:
		return c.createNode(node, &JSXClosingFragment{
			Type: ESTreeKindJSXClosingFragment,
		})

	case ast.KindJsxExpression:
		n := node.AsJsxExpression()
		var expression NodeWithRange
		if n.Expression != nil {
			expression = c.convertChild(n.Expression, nil)
		} else {
			expression = c.createNode(node, &JSXEmptyExpression{
				Type:  ESTreeKindJSXEmptyExpression,
				Range: Range{c.getNodeStart(node) + 1, node.End() - 1},
			})
		}

		if n.DotDotDotToken != nil {
			return c.createNode(node, &JSXSpreadChild{
				Type:       ESTreeKindJSXSpreadChild,
				Expression: expression,
			})
		}

		return c.createNode(node, &JSXExpressionContainer{
			Type:       ESTreeKindJSXExpressionContainer,
			Expression: expression,
		})

	case ast.KindJsxAttribute:
		n := node.AsJsxAttribute()
		return c.createNode(node, &JSXAttribute{
			Type:  ESTreeKindJSXAttribute,
			Name:  c.convertJSXNamespaceOrIdentifier(n.Name()),
			Value: c.convertChild(n.Initializer, nil),
		})

	case ast.KindJsxText:
		start := node.Pos()
		end := node.End()
		text := c.sourceFile.Text[start:end]

		return c.createNode(node, &JSXText{
			Type:  ESTreeKindJSXText,
			Range: Range{start, end},
			Raw:   text,
			Value: text, // TODO(port): unescapeStringLiteralText(text),
		})

	case ast.KindJsxSpreadAttribute:
		n := node.AsJsxSpreadAttribute()
		return c.createNode(node, &JSXSpreadAttribute{
			Type:     ESTreeKindJSXSpreadAttribute,
			Argument: c.convertChild(n.Expression, nil),
		})

	case ast.KindQualifiedName:
		n := node.AsQualifiedName()
		return c.createNode(node, &TSQualifiedName{
			Type:  ESTreeKindTSQualifiedName,
			Left:  c.convertChild(n.Left, nil),
			Right: c.convertChild(n.Right, nil).(*Identifier),
		})

	// TypeScript specific

	case ast.KindTypeReference:
		n := node.AsTypeReference()
		return c.createNode(node, &TSTypeReference{
			Type:          ESTreeKindTSTypeReference,
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
			TypeName:      c.convertChild(n.TypeName, nil),
		})

	case ast.KindTypeParameter:
		n := node.AsTypeParameter()
		return c.createNode(node, &TSTypeParameter{
			Type:       ESTreeKindTSTypeParameter,
			Const:      hasModifier(ast.KindConstKeyword, node),
			Constraint: c.convertChild(n.Constraint, nil),
			Default:    c.convertChild(n.DefaultType, nil),
			In:         hasModifier(ast.KindInKeyword, node),
			Name:       c.convertChild(n.Name(), nil).(*Identifier),
			Out:        hasModifier(ast.KindOutKeyword, node),
		})

	case ast.KindThisType:
		return c.createNode(node, &TSThisType{
			Type: ESTreeKindTSThisType,
		})
	case ast.KindAnyKeyword:
		return c.createNode(node, &TSAnyKeyword{
			Type: ESTreeKindTSAnyKeyword,
		})
	case ast.KindBigIntKeyword:
		return c.createNode(node, &TSBigIntKeyword{
			Type: ESTreeKindTSBigIntKeyword,
		})
	case ast.KindBooleanKeyword:
		return c.createNode(node, &TSBooleanKeyword{
			Type: ESTreeKindTSBooleanKeyword,
		})
	case ast.KindNeverKeyword:
		return c.createNode(node, &TSNeverKeyword{
			Type: ESTreeKindTSNeverKeyword,
		})
	case ast.KindNumberKeyword:
		return c.createNode(node, &TSNumberKeyword{
			Type: ESTreeKindTSNumberKeyword,
		})
	case ast.KindObjectKeyword:
		return c.createNode(node, &TSObjectKeyword{
			Type: ESTreeKindTSObjectKeyword,
		})
	case ast.KindStringKeyword:
		return c.createNode(node, &TSStringKeyword{
			Type: ESTreeKindTSStringKeyword,
		})
	case ast.KindSymbolKeyword:
		return c.createNode(node, &TSSymbolKeyword{
			Type: ESTreeKindTSSymbolKeyword,
		})
	case ast.KindUnknownKeyword:
		return c.createNode(node, &TSUnknownKeyword{
			Type: ESTreeKindTSUnknownKeyword,
		})
	case ast.KindVoidKeyword:
		return c.createNode(node, &TSVoidKeyword{
			Type: ESTreeKindTSVoidKeyword,
		})
	case ast.KindUndefinedKeyword:
		return c.createNode(node, &TSUndefinedKeyword{
			Type: ESTreeKindTSUndefinedKeyword,
		})
	case ast.KindIntrinsicKeyword:
		return c.createNode(node, &TSIntrinsicKeyword{
			Type: ESTreeKindTSIntrinsicKeyword,
		})

	case ast.KindNonNullExpression:
		n := node.AsNonNullExpression()
		nnExpr := c.createNode(node, &TSNonNullExpression{
			Type:       ESTreeKindTSNonNullExpression,
			Expression: c.convertChild(n.Expression, nil),
		})

		return c.convertChainExpression(nnExpr, node)

	case ast.KindTypeLiteral:
		n := node.AsTypeLiteralNode()
		return c.createNode(node, &TSTypeLiteral{
			Type:    ESTreeKindTSTypeLiteral,
			Members: convertNodeListToChildren[any](c, n.Members),
		})

	case ast.KindArrayType:
		n := node.AsArrayTypeNode()
		return c.createNode(node, &TSArrayType{
			Type:        ESTreeKindTSArrayType,
			ElementType: c.convertChild(n.ElementType, nil),
		})

	case ast.KindIndexedAccessType:
		n := node.AsIndexedAccessTypeNode()
		return c.createNode(node, &TSIndexedAccessType{
			Type:       ESTreeKindTSIndexedAccessType,
			IndexType:  c.convertChild(n.IndexType, nil),
			ObjectType: c.convertChild(n.ObjectType, nil),
		})

	case ast.KindConditionalType:
		n := node.AsConditionalTypeNode()
		return c.createNode(node, &TSConditionalType{
			Type:        ESTreeKindTSConditionalType,
			CheckType:   c.convertChild(n.CheckType, nil),
			ExtendsType: c.convertChild(n.ExtendsType, nil),
			FalseType:   c.convertChild(n.FalseType, nil),
			TrueType:    c.convertChild(n.TrueType, nil),
		})

	case ast.KindTypeQuery:
		n := node.AsTypeQueryNode()
		return c.createNode(node, &TSTypeQuery{
			Type:          ESTreeKindTSTypeQuery,
			ExprName:      c.convertChild(n.ExprName, nil),
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

	case ast.KindMappedType:
		n := node.AsMappedTypeNode()
		typeParameter := n.TypeParameter.AsTypeParameter()

		var optional any
		if n.QuestionToken != nil {
			if n.QuestionToken.Kind == ast.KindQuestionToken {
				optional = true
			} else {
				optional = scanner.TokenToString(n.QuestionToken.Kind)
			}
		}

		var readonly any
		if n.ReadonlyToken != nil {
			if n.ReadonlyToken.Kind == ast.KindReadonlyKeyword {
				readonly = true
			} else {
				readonly = scanner.TokenToString(n.ReadonlyToken.Kind)
			}
		}

		return c.createNode(node, &TSMappedType{
			Type:           ESTreeKindTSMappedType,
			Constraint:     c.convertChild(typeParameter.Constraint, nil),
			Key:            c.convertChild(typeParameter.Name(), nil).(*Identifier),
			NameType:       c.convertChild(n.NameType, nil),
			Optional:       optional,
			Readonly:       readonly,
			TypeAnnotation: c.convertChild(n.Type, nil),
		})

	case ast.KindParenthesizedExpression:
		n := node.AsParenthesizedExpression()
		return c.convertChild(n.Expression, parent)

	case ast.KindTypeAliasDeclaration:
		n := node.AsTypeAliasDeclaration()
		result := c.createNode(node, &TSTypeAliasDeclaration{
			Type:           ESTreeKindTSTypeAliasDeclaration,
			Declare:        hasModifier(ast.KindDeclareKeyword, node),
			Id:             c.convertChild(n.Name(), nil).(*Identifier),
			TypeAnnotation: c.convertChild(n.Type, nil),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

		return c.fixExports(node, result)

	case ast.KindMethodSignature:
		return c.convertMethodSignature(node)

	case ast.KindPropertySignature:
		n := node.AsPropertySignatureDeclaration()
		name := n.Name()
		return c.createNode(node, &TSPropertySignature{
			Type:           ESTreeKindTSPropertySignature,
			Accessibility:  getTSNodeAccessibility(node),
			Computed:       ast.IsComputedPropertyName(name),
			Key:            c.convertChild(name, nil),
			Optional:       n.PostfixToken != nil && n.PostfixToken.Kind == ast.KindQuestionToken,
			Readonly:       hasModifier(ast.KindReadonlyKeyword, node),
			Static:         hasModifier(ast.KindStaticKeyword, node),
			TypeAnnotation: c.convertTypeAnnotation(n.Type, node),
		})

	case ast.KindIndexSignature:
		n := node.AsIndexSignatureDeclaration()
		return c.createNode(node, &TSIndexSignature{
			Type:           ESTreeKindTSIndexSignature,
			Accessibility:  getTSNodeAccessibility(node),
			Parameters:     convertNodeListToChildren[any](c, n.Parameters),
			Readonly:       hasModifier(ast.KindReadonlyKeyword, node),
			Static:         hasModifier(ast.KindStaticKeyword, node),
			TypeAnnotation: c.convertTypeAnnotation(n.Type, node),
		})

	case ast.KindConstructorType:
		n := node.AsConstructorTypeNode()
		return c.createNode(node, &TSConstructorType{
			Type:           ESTreeKindTSConstructorType,
			Abstract:       hasModifier(ast.KindAbstractKeyword, node),
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

	case ast.KindFunctionType:
		n := node.AsFunctionTypeNode()
		return c.createNode(node, &TSFunctionType{
			Type:           ESTreeKindTSFunctionType,
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})
	case ast.KindConstructSignature:
		n := node.AsConstructSignatureDeclaration()
		return c.createNode(node, &TSConstructSignatureDeclaration{
			Type:           ESTreeKindTSConstructSignatureDeclaration,
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})
	case ast.KindCallSignature:
		n := node.AsCallSignatureDeclaration()
		return c.createNode(node, &TSCallSignatureDeclaration{
			Type:           ESTreeKindTSCallSignatureDeclaration,
			Params:         c.convertParameters(n.Parameters),
			ReturnType:     c.convertTypeAnnotation(n.Type, node),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

	case ast.KindExpressionWithTypeArguments:
		n := node.AsExpressionWithTypeArguments()
		expression := c.convertChild(n.Expression, nil)
		typeArguments := c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node)

		switch parent.Kind {
		case ast.KindInterfaceDeclaration:
			return c.createNode(node, &TSInterfaceHeritage{
				Type:          ESTreeKindTSInterfaceHeritage,
				Expression:    expression,
				TypeArguments: typeArguments,
			})
		case ast.KindHeritageClause:
			return c.createNode(node, &TSClassImplements{
				Type:          ESTreeKindTSClassImplements,
				Expression:    expression,
				TypeArguments: typeArguments,
			})
		default:
			return c.createNode(node, &TSInstantiationExpression{
				Type:          ESTreeKindTSInstantiationExpression,
				Expression:    expression,
				TypeArguments: typeArguments,
			})
		}

	case ast.KindInterfaceDeclaration:
		n := node.AsInterfaceDeclaration()
		interfaceExtends := []*TSInterfaceHeritage{}
		if n.HeritageClauses != nil {
			for _, heritageClause := range n.HeritageClauses.Nodes {
				h := heritageClause.AsHeritageClause()
				for _, heritageType := range h.Types.Nodes {
					interfaceExtends = append(interfaceExtends, c.convertChild(heritageType, node).(*TSInterfaceHeritage))
				}
			}
		}

		result := c.createNode(node, &TSInterfaceDeclaration{
			Type: ESTreeKindTSInterfaceDeclaration,
			Body: c.createNode(node, &TSInterfaceBody{
				Type:  ESTreeKindTSInterfaceBody,
				Range: Range{n.Members.Pos() - 1, node.End()},
				Body:  convertNodeListToChildren[any](c, n.Members),
			}).(*TSInterfaceBody),
			Declare:        hasModifier(ast.KindDeclareKeyword, node),
			Extends:        interfaceExtends,
			Id:             c.convertChild(n.Name(), nil).(*Identifier),
			TypeParameters: c.convertTSTypeParametersToTypeParametersDeclaration(n.TypeParameters),
		})

		return c.fixExports(node, result)

	case ast.KindTypePredicate:
		n := node.AsTypePredicateNode()
		var typeAnnotation *TSTypeAnnotation
		/**
		 * Specific fix for type-guard location data
		 */
		if n.Type != nil {
			typeAnnotation = c.convertTypeAnnotation(n.Type, node)
			typeAnnotation.Loc = typeAnnotation.TypeAnnotation.(NodeWithRange).GetLoc()
			typeAnnotation.Range = typeAnnotation.TypeAnnotation.(NodeWithRange).GetRange()
		}
		return c.createNode(node, &TSTypePredicate{
			Type:           ESTreeKindTSTypePredicate,
			Asserts:        n.AssertsModifier != nil,
			ParameterName:  c.convertChild(n.ParameterName, nil),
			TypeAnnotation: typeAnnotation,
		})

	case ast.KindImportType:
		n := node.AsImportTypeNode()

		r := c.getRange(node)
		if n.IsTypeOf {
			r[0] = scanner.GetRangeOfTokenAtPosition(c.sourceFile, scanner.GetRangeOfTokenAtPosition(c.sourceFile, r[0]).End()).Pos()
		}

		var options *ObjectExpression

		if n.Attributes != nil {
			value := c.createNode(n.Attributes, &ObjectExpression{
				Type: ESTreeKindObjectExpression,
				Properties: Map(n.Attributes.AsImportAttributes().Attributes.Nodes, func(importAttribute *ast.Node) any {
					i := importAttribute.AsImportAttribute()
					return c.createNode(importAttribute, &Property{
						Type:      ESTreeKindProperty,
						Computed:  false,
						Key:       c.convertChild(i.Name(), nil),
						Kind:      "init",
						Method:    false,
						Optional:  false,
						Shorthand: false,
						Value:     c.convertChild(i.Value, nil),
					})
				}),
			})

			commaToken := scanner.GetRangeOfTokenAtPosition(c.sourceFile, n.Argument.End())
			openBraceToken := scanner.GetRangeOfTokenAtPosition(c.sourceFile, commaToken.End())
			closeBraceToken := scanner.GetRangeOfTokenAtPosition(c.sourceFile, n.Attributes.Loc.End())
			withToken := scanner.GetRangeOfTokenAtPosition(c.sourceFile, openBraceToken.End())
			withTokenRange := Range{withToken.Pos(), withToken.End()}

			options = c.createNode(node, &ObjectExpression{
				Type:  ESTreeKindObjectExpression,
				Range: Range{openBraceToken.Pos(), closeBraceToken.End()},
				Properties: []any{
					c.createNode(node, &Property{
						Type:     ESTreeKindProperty,
						Range:    Range{withTokenRange[0], n.Attributes.End()},
						Computed: false,
						Key: c.createNode(node, &Identifier{
							Type:           ESTreeKindIdentifier,
							Range:          withTokenRange,
							Decorators:     []*Decorator{},
							Name:           "with",
							Optional:       false,
							TypeAnnotation: nil,
						}),
						Kind:      "init",
						Method:    false,
						Optional:  false,
						Shorthand: false,
						Value:     value,
					}),
				},
			}).(*ObjectExpression)
		}

		result := c.createNode(node, &TSImportType{
			Type:          ESTreeKindTSImportType,
			Range:         r,
			Argument:      c.convertChild(n.Argument, nil),
			Options:       options,
			Qualifier:     c.convertChild(n.Qualifier, nil),
			TypeArguments: c.convertTypeArgumentsToTypeParameterInstantiation(n.TypeArguments, node),
		})

		if n.IsTypeOf {
			return c.createNode(node, &TSTypeQuery{
				Type:          ESTreeKindTSTypeQuery,
				ExprName:      result,
				TypeArguments: nil,
			})
		}
		return result

	case ast.KindEnumDeclaration:
		n := node.AsEnumDeclaration()

		result := c.createNode(node, &TSEnumDeclaration{
			Type: ESTreeKindTSEnumDeclaration,
			Body: c.createNode(node, &TSEnumBody{
				Type:    ESTreeKindTSEnumBody,
				Range:   Range{n.Members.Pos() - 1, node.End()},
				Members: convertNodeListToChildren[any](c, n.Members),
			}).(*TSEnumBody),
			Const:   hasModifier(ast.KindConstKeyword, node),
			Declare: hasModifier(ast.KindDeclareKeyword, node),
			Id:      c.convertChild(n.Name(), nil).(*Identifier),
		})
		return c.fixExports(node, result)

	case ast.KindEnumMember:
		n := node.AsEnumMember()
		return c.createNode(node, &TSEnumMember{
			Type:        ESTreeKindTSEnumMember,
			Computed:    n.Name().Kind == ast.KindComputedPropertyName,
			Id:          c.convertChild(n.Name(), nil),
			Initializer: c.convertChild(n.Initializer, nil),
		})

	case ast.KindModuleDeclaration:
		n := node.AsModuleDeclaration()

		isDeclare := hasModifier(ast.KindDeclareKeyword, node)
		name := node.Name()

		var id NodeWithRange
		var kind string

		if ast.IsGlobalScopeAugmentation(node) {
			id = c.convertChild(name, nil)
			kind = "global"
		} else if ast.IsStringLiteral(name) {
			id = c.convertChild(name, nil)
			kind = "module"
		} else {
			// Nested module declarations are stored in TypeScript as nested tree nodes.
			// We "unravel" them here by making our own nested TSQualifiedName,
			// with the innermost node's body as the actual node body.
			nameRes := c.createNode(name, &Identifier{
				Type:           ESTreeKindIdentifier,
				Decorators:     []*Decorator{},
				Name:           name.Text(),
				Optional:       false,
				TypeAnnotation: nil,
			})

			for n.Body != nil && ast.IsModuleDeclaration(n.Body) && n.Body.AsModuleDeclaration().Name() != nil {
				n = n.Body.AsModuleDeclaration()

				isDeclare = isDeclare || hasModifier(ast.KindDeclareKeyword, n)
				nextName := n.Name()

				right := c.createNode(nextName, &Identifier{
					Type:           ESTreeKindIdentifier,
					Decorators:     []*Decorator{},
					Name:           nextName.Text(),
					Optional:       false,
					TypeAnnotation: nil,
				})

				nameRes = c.createNode(nextName, &TSQualifiedName{
					Type:  ESTreeKindTSQualifiedName,
					Range: Range{nameRes.GetRange()[0], right.GetRange()[1]},
					Left:  nameRes,
					Right: right.(*Identifier),
				})
			}

			id = nameRes
			kind = "module"
			if n.Keyword == ast.KindNamespaceKeyword {
				kind = "namespace"
			}
		}
		body := convertChildT[*TSModuleBlock](c, n.Body, nil)

		result := c.createNode(node, &TSModuleDeclaration{
			Type:    ESTreeKindTSModuleDeclaration,
			Body:    body,
			Global:  n.Keyword == ast.KindGlobalKeyword,
			Declare: isDeclare,
			Id:      id,
			Kind:    kind,
		})

		return c.fixExports(node, result)

	// TypeScript specific types
	case ast.KindParenthesizedType:
		return c.convertChild(node.AsParenthesizedTypeNode().Type, nil)
	case ast.KindUnionType:
		n := node.AsUnionTypeNode()
		return c.createNode(node, &TSUnionType{
			Type:  ESTreeKindTSUnionType,
			Types: convertNodeListToChildren[any](c, n.Types),
		})

	case ast.KindIntersectionType:
		n := node.AsIntersectionTypeNode()
		return c.createNode(node, &TSIntersectionType{
			Type:  ESTreeKindTSIntersectionType,
			Types: convertNodeListToChildren[any](c, n.Types),
		})
	case ast.KindAsExpression:
		n := node.AsAsExpression()
		return c.createNode(node, &TSAsExpression{
			Type:           ESTreeKindTSAsExpression,
			Expression:     c.convertChild(n.Expression, nil),
			TypeAnnotation: c.convertChild(n.Type, nil),
		})
	case ast.KindInferType:
		n := node.AsInferTypeNode()
		return c.createNode(node, &TSInferType{
			Type:          ESTreeKindTSInferType,
			TypeParameter: c.convertChild(n.TypeParameter, nil).(*TSTypeParameter),
		})
	case ast.KindLiteralType:
		n := node.AsLiteralTypeNode()
		if n.Literal.Kind == ast.KindNullKeyword {
			// 4.0 started nesting null types inside a LiteralType node
			// but our AST is designed around the old way of null being a keyword
			return c.createNode(n.Literal, &TSNullKeyword{
				Type: ESTreeKindTSNullKeyword,
			})
		}

		return c.createNode(node, &TSLiteralType{
			Type:    ESTreeKindTSLiteralType,
			Literal: c.convertChild(n.Literal, nil),
		})
	case ast.KindTypeAssertionExpression:
		n := node.AsTypeAssertion()
		return c.createNode(node, &TSTypeAssertion{
			Type:           ESTreeKindTSTypeAssertion,
			Expression:     c.convertChild(n.Expression, nil),
			TypeAnnotation: c.convertChild(n.Type, nil),
		})
	case ast.KindImportEqualsDeclaration:
		n := node.AsImportEqualsDeclaration()
		importKind := "value"
		if n.IsTypeOnly {
			importKind = "type"
		}
		return c.fixExports(node, c.createNode(node, &TSImportEqualsDeclaration{
			Type:            ESTreeKindTSImportEqualsDeclaration,
			Id:              c.convertChild(n.Name(), nil).(*Identifier),
			ImportKind:      importKind,
			ModuleReference: c.convertChild(n.ModuleReference, nil),
		}))
	case ast.KindExternalModuleReference:
		n := node.AsExternalModuleReference()
		return c.createNode(node, &TSExternalModuleReference{
			Type:       ESTreeKindTSExternalModuleReference,
			Expression: c.convertChild(n.Expression, nil).(*Literal),
		})
	case ast.KindNamespaceExportDeclaration:
		n := node.AsNamespaceExportDeclaration()
		return c.createNode(node, &TSNamespaceExportDeclaration{
			Type: ESTreeKindTSNamespaceExportDeclaration,
			Id:   c.convertChild(n.Name(), nil).(*Identifier),
		})
	case ast.KindAbstractKeyword:
		return c.createNode(node, &TSAbstractKeyword{
			Type: ESTreeKindTSAbstractKeyword,
		})

	// Tuple
	case ast.KindTupleType:
		n := node.AsTupleTypeNode()

		return c.createNode(node, &TSTupleType{
			Type:         ESTreeKindTSTupleType,
			ElementTypes: convertNodeListToChildren[any](c, n.Elements),
		})
	case ast.KindNamedTupleMember:
		n := node.AsNamedTupleMember()

		label := c.convertChild(n.Name(), node)
		member := c.createNode(node, &TSNamedTupleMember{
			Type:        ESTreeKindTSNamedTupleMember,
			ElementType: c.convertChild(n.Type, node),
			Label:       label.(*Identifier),
			Optional:    n.QuestionToken != nil,
		})

		if n.DotDotDotToken != nil {
			// adjust the start to account for the "..."
			member.setRange(Range{label.GetRange()[0], member.GetRange()[1]})
			member.setLoc(SourceLocation{
				Start: label.GetLoc().Start,
				End:   member.GetLoc().End,
			})
			return c.createNode(node, &TSRestType{
				Type:           ESTreeKindTSRestType,
				TypeAnnotation: member,
			})
		}

		return member
	case ast.KindOptionalType:
		n := node.AsOptionalTypeNode()
		return c.createNode(node, &TSOptionalType{
			Type:           ESTreeKindTSOptionalType,
			TypeAnnotation: c.convertChild(n.Type, nil),
		})
	case ast.KindRestType:
		n := node.AsRestTypeNode()
		return c.createNode(node, &TSRestType{
			Type:           ESTreeKindTSRestType,
			TypeAnnotation: c.convertChild(n.Type, nil),
		})

	// Template Literal Types
	case ast.KindTemplateLiteralType:
		n := node.AsTemplateLiteralTypeNode()
		types := []any{}
		quasis := []*TemplateElement{c.convertChild(n.Head, nil).(*TemplateElement)}

		for _, templateSpan := range n.TemplateSpans.Nodes {
			t := templateSpan.AsTemplateLiteralTypeSpan()
			types = append(types, c.convertChild(t.Type, nil))
			quasis = append(quasis, c.convertChild(t.Literal, nil).(*TemplateElement))
		}

		return c.createNode(node, &TSTemplateLiteralType{
			Type:   ESTreeKindTSTemplateLiteralType,
			Quasis: quasis,
			Types:  types,
		})

	case ast.KindClassStaticBlockDeclaration:
		n := node.AsClassStaticBlockDeclaration()
		return c.createNode(node, &StaticBlock{
			Type: ESTreeKindStaticBlock,
			Body: c.convertBodyExpressions(n.Body.AsBlock().Statements, node),
		})

	case ast.KindImportAttribute:
		n := node.AsImportAttribute()
		return c.createNode(node, &ImportAttribute{
			Type:  ESTreeKindImportAttribute,
			Key:   c.convertChild(n.Name(), nil),
			Value: c.convertChild(n.Value, nil),
		})

	case ast.KindSatisfiesExpression:
		n := node.AsSatisfiesExpression()
		return c.createNode(node, &TSSatisfiesExpression{
			Type:           ESTreeKindTSSatisfiesExpression,
			Expression:     c.convertChild(n.Expression, nil),
			TypeAnnotation: c.convertChild(n.Type, nil),
		})
	}
	panic("unknown TS AST node kind")
}

func getTokenType(token tsToken, tokenText string) ESTreeKind {
	if token.kind == ast.KindIdentifier {
		keywordKind := scanner.GetIdentifierToken(tokenText)

		if keywordKind != ast.KindIdentifier {
			if keywordKind == ast.KindNullKeyword {
				return ESTreeTokenTypeNull
			}

			if keywordKind >= ast.KindFirstFutureReservedWord && keywordKind <= ast.KindLastKeyword {
				return ESTreeTokenTypeIdentifier
			}
			return ESTreeTokenTypeKeyword
		}
	}

	if token.kind >= ast.KindFirstKeyword && token.kind <= ast.KindLastFutureReservedWord {
		if token.kind == ast.KindFalseKeyword || token.kind == ast.KindTrueKeyword {
			return ESTreeTokenTypeBoolean
		}
		return ESTreeTokenTypeKeyword
	}

	if token.kind >= ast.KindFirstPunctuation && token.kind <= ast.KindLastPunctuation {
		return ESTreeTokenTypePunctuator
	}

	if token.kind >= ast.KindFirstTemplateToken && token.kind <= ast.KindLastTemplateToken {
		return ESTreeTokenTypeTemplate
	}

	switch token.kind {
	case ast.KindNumericLiteral, ast.KindBigIntLiteral:
		return ESTreeTokenTypeNumeric
	case ast.KindPrivateIdentifier:
		return ESTreeTokenTypePrivateIdentifier
	case ast.KindJsxText:
		return ESTreeTokenTypeJSXText
	case ast.KindStringLiteral:
		// TODO(port)
		// A TypeScript-StringLiteral token with a TypeScript-JsxAttribute or TypeScript-JsxElement parent,
		// must actually be an ESTree-JSXText token
		if token.parent != nil && (token.parent.Kind == ast.KindJsxAttribute || token.parent.Kind == ast.KindJsxElement) {
			return ESTreeTokenTypeJSXText
		}
		return ESTreeTokenTypeString
	case ast.KindRegularExpressionLiteral:
		return ESTreeTokenTypeRegularExpression
	}
	// Some JSX tokens have to be determined based on their parent
	if token.kind == ast.KindIdentifier && token.parent != nil {
		if isJSXToken(token.parent) {
			return ESTreeTokenTypeJSXIdentifier
		}

		if token.parent.Kind == ast.KindPropertyAccessExpression && hasJSXAncestor(token.parent) {
			return ESTreeTokenTypeJSXIdentifier
		}
	}
	return ESTreeTokenTypeIdentifier
}

func isJSXToken(node *ast.Node) bool {
	return node.Kind >= ast.KindJsxElement && node.Kind <= ast.KindJsxAttribute
}

func hasJSXAncestor(node *ast.Node) bool {
	for node != nil {
		if isJSXToken(node) {
			return true
		}
		node = node.Parent
	}

	return false
}

type tsToken struct {
	kind   ast.Kind
	loc    core.TextRange
	parent *ast.Node
}

func (c *converter) collectTokens() (tokens []tsToken, comments []NodeWithRange) {
	tokens = []tsToken{}
	comments = []NodeWithRange{}

	s := scanner.NewScanner()
	s.SetText(c.sourceFile.Text)
	s.SetScriptTarget(c.sourceFile.LanguageVersion)
	s.SetLanguageVariant(c.sourceFile.LanguageVariant)
	s.SetSkipTrivia(true)

	pos := 0
	hasShebang := false

	for i, char := range c.sourceFile.Text {
		if i == 0 {
			if char == '#' {
				continue
			}
			break
		} else if i == 1 {
			if char == '!' {
				hasShebang = true
				continue
			}
			break
		}

		if stringutil.IsLineBreak(char) {
			pos = i

			r := Range{2, pos}
			loc := c.getLocFor(r)

			comments = append(comments, &LineComment{
				Type:  ESTreeTokenTypeShebang,
				Value: c.sourceFile.Text[2:pos],
				Range: r,
				Loc:   loc,
			})

			break
		}
	}
	// no line break at the end
	if hasShebang && len(comments) == 0 {
		pos = len(c.sourceFile.Text)
		r := Range{2, pos}
		loc := c.getLocFor(r)

		comments = append(comments, &LineComment{
			Type:  ESTreeTokenTypeShebang,
			Value: c.sourceFile.Text[2:pos],
			Range: r,
			Loc:   loc,
		})
	}

	scanCommentsInRange := func(r core.TextRange) {
		for cm := range utils.GetCommentsInRange(c.sourceFile, r) {
			r := Range{cm.Pos(), cm.End()}
			loc := c.getLocFor(r)
			// both comments start with 2 characters - /* or //
			textStart := r[0] + 2

			if cm.Kind == ast.KindSingleLineCommentTrivia {
				comments = append(comments, &LineComment{
					Type:  ESTreeTokenTypeLine,
					Value: c.sourceFile.Text[textStart:r[1]],
					Range: r,
					Loc:   loc,
				})
			} else {
				comments = append(comments, &BlockComment{
					Type:  ESTreeTokenTypeBlock,
					Value: c.sourceFile.Text[textStart : r[1]-2],
					Range: r,
					Loc:   loc,
				})
			}
		}
	}

	pushToken := func(t tsToken) {
		prevTokenEnd := 0
		if len(tokens) != 0 {
			prevTokenEnd = tokens[len(tokens)-1].loc.End()
		}

		tokens = append(tokens, t)

		scanCommentsInRange(core.NewTextRange(prevTokenEnd, t.loc.Pos()))
	}

	addSyntheticNodes := func(end int) {
		s.ResetPos(pos)

		for pos < end {
			s.Scan()
			textPos := s.TokenEnd()
			if textPos <= end && ast.IsTokenKind(s.Token()) {
				kind := s.Token()
				// TODO: for some reason tseslint+Strada parse </ as two distinct tokens
				if kind == ast.KindLessThanSlashToken {
					r := s.TokenRange()
					pushToken(tsToken{
						kind: ast.KindLessThanToken,
						loc:  r.WithEnd(r.End() - 1),
					})
					pushToken(tsToken{
						kind: ast.KindSlashToken,
						loc:  r.WithPos(r.End() - 1),
					})
				} else {
					pushToken(tsToken{
						kind: s.Token(),
						loc:  s.TokenRange(),
					})
				}
			}
			pos = textPos
			if s.Token() == ast.KindEndOfFile {
				break
			}
		}
	}

	var visit ast.Visitor
	visit = func(node *ast.Node) bool {
		node.ForEachChild(visit)

		addSyntheticNodes(node.Pos())

		if ast.IsTokenKind(node.Kind) && node.Flags&ast.NodeFlagsReparsed == 0 {
			if node.Kind != ast.KindJsxText {
				s.ResetPos(node.Pos())
				s.Scan()
			}
			pushToken(tsToken{
				kind:   node.Kind,
				loc:    node.Loc.WithPos(s.TokenStart()),
				parent: node.Parent,
			})
			pos = node.End()
		}

		addSyntheticNodes(node.End())

		pos = node.End()

		return false
	}
	c.sourceFile.ForEachChild(visit)
	scanCommentsInRange(c.sourceFile.Loc.WithPos(pos))

	return tokens, comments
}

func (c *converter) parseTokens() (tokens []NodeWithRange, comments []NodeWithRange) {
	s := scanner.GetScannerForSourceFile(c.sourceFile, 0)

	tokens = []NodeWithRange{}

	tsTokens, comments := c.collectTokens()
	for _, token := range tsTokens {
		start, end := token.loc.Pos(), token.loc.End()
		value := c.sourceFile.Text[start:end]
		tokenType := getTokenType(token, value)
		r := Range{start, end}
		loc := c.getLocFor(r)

		var token NodeWithRange
		switch tokenType {
		case ESTreeTokenTypeRegularExpression:
			terminatorIndex := strings.LastIndex(value, "/")
			pattern := value[1:terminatorIndex]
			flags := value[terminatorIndex+1:]
			token = &RegularExpressionToken{
				Type: tokenType,
				Regex: struct {
					Pattern string
					Flags   string
				}{pattern, flags},
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeBoolean:
			token = &BooleanToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeIdentifier:
			token = &IdentifierToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypePrivateIdentifier:
			token = &PrivateIdentifierToken{
				Type:  tokenType,
				Value: value[1:],
				// TODO: if we strip # from value, maybe we should adjust range&loc too?
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeJSXIdentifier:
			token = &JSXIdentifierToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeJSXText:
			token = &JSXTextToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeKeyword:
			token = &KeywordToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeNull:
			token = &NullToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeNumeric:
			token = &NumericToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypePunctuator:
			token = &PunctuatorToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeString:
			token = &StringToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		case ESTreeTokenTypeTemplate:
			token = &TemplateToken{
				Type:  tokenType,
				Value: value,
				Loc:   loc,
				Range: r,
			}
		default:
			panic("unhandled token type")
		}
		tokens = append(tokens, token)
		s.Scan()
	}
	return tokens, comments
}
