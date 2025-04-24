package estree

//go:generate node --run tools:gen-traverser

type traverser struct {
	onEnter func(NodeWithRange)
	onExit  func(NodeWithRange)
}

func TraverseAst(ast NodeWithRange, onEnter func(node NodeWithRange), onExit func(node NodeWithRange)) {
	t := traverser{
		onEnter,
		onExit,
	}

	t.traverse(ast, nil)
}

func (t *traverser) traverse(node any, parent NodeWithRange) {
	n := node.(NodeWithRange)
	n.SetParent(parent)

	t.onEnter(n)

	t.traverseInner(n)

	// TODO
	// t.onExit(n)
}
