package core

import (
	"reflect"

	"github.com/pingcap/parser/model"
	"github.com/pingcap/tidb/expression"
)

// TableInfoSets includes in the table's sets for forming virtual indices.
type TableInfoSets struct {
	TblInfo *model.TableInfo
	Eq      []model.CIStr
	O       [][]model.CIStr
	Rg      []model.CIStr
	Ref     []model.CIStr
}

// List set names.
const (
	EQ    = "eq"
	RANGE = "rg"
	REF   = "ref"
)

// QueryExprInfo includes in a query's Column and ScalarFunction.
type QueryExprInfo struct {
	ScalarFuncExpr []*expression.ScalarFunction
	ColumnExpr     [][]*expression.Column
	ProjExpr       []expression.Expression
	Ds             []*dataSource
}

type dataSource struct {
	Table       *model.TableInfo
	TableAsName *model.CIStr
	ColCnt      int
}

func multiJoinQueryExprInfo(queryInfos []*QueryExprInfo) *QueryExprInfo {
	if len(queryInfos) == 1 {
		return queryInfos[0]
	}

	q := queryInfos[0]
	for i := 1; i < len(queryInfos); i++ {
		q = doubleJoinQueryExprInfo(q, queryInfos[i])
	}
	return q
}

func doubleJoinQueryExprInfo(first *QueryExprInfo, next *QueryExprInfo) *QueryExprInfo {
	first.ScalarFuncExpr = append(first.ScalarFuncExpr, next.ScalarFuncExpr...)
	first.ColumnExpr = append(first.ColumnExpr, next.ColumnExpr...)
	first.ProjExpr = append(first.ProjExpr, next.ProjExpr...)
	first.Ds = append(first.Ds, next.Ds...)
	return first
}

// NewQueryExprInfo constructs the expression information of the query.
func NewQueryExprInfo(p PhysicalPlan) *QueryExprInfo {
	queryInfos, _ := recursiveGenQueryInfo(p, []*QueryExprInfo{}, []int{})
	return multiJoinQueryExprInfo(queryInfos)
}

func recursiveGenQueryInfo(in PhysicalPlan, queryInfos []*QueryExprInfo, idxs []int) ([]*QueryExprInfo, []int) {
	if len(in.Children()) > 1 {
		idxs = append(idxs, len(queryInfos))
	}

	for _, c := range in.Children() {
		queryInfos, idxs = recursiveGenQueryInfo(c, queryInfos, idxs)
	}

	queryInfo := new(QueryExprInfo)
	switch x := in.(type) {
	case *PhysicalIndexScan:
		ds := &dataSource{Table: x.Table, TableAsName: x.TableAsName, ColCnt: len(x.Columns)}
		queryInfo.Ds = append(queryInfo.Ds, ds)
	case *PhysicalTableScan:
		ds := &dataSource{Table: x.Table, TableAsName: x.TableAsName, ColCnt: len(x.Columns)}
		queryInfo.Ds = append(queryInfo.Ds, ds)
	case *PhysicalHashJoin:
		last := len(idxs) - 1
		idx := idxs[last]
		children := queryInfos[idx:]
		queryInfos = queryInfos[:idx]
		idxs = idxs[:last]
		queryInfo = multiJoinQueryExprInfo(children)
		for _, eq := range x.EqualConditions {
			l := eq.GetArgs()[0]
			r := eq.GetArgs()[1]
			if lc, ok := l.(*expression.Column); ok {
				queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{lc})
			}
			if rc, ok := r.(*expression.Column); ok {
				queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{rc})
			}
		}
		for _, e := range x.OtherConditions {
			switch expr := e.(type) {
			case *expression.ScalarFunction:
				queryInfo.ScalarFuncExpr = append(queryInfo.ScalarFuncExpr, expr)
			}
		}
	case *PhysicalMergeJoin:
		last := len(idxs) - 1
		idx := idxs[last]
		children := queryInfos[idx:]
		queryInfos = queryInfos[:idx]
		idxs = idxs[:last]
		queryInfo = multiJoinQueryExprInfo(children)
		for i := range x.LeftKeys {
			l := x.LeftKeys[i]
			r := x.RightKeys[i]
			queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{l}, []*expression.Column{r})
		}
		for _, e := range x.OtherConditions {
			switch expr := e.(type) {
			case *expression.ScalarFunction:
				queryInfo.ScalarFuncExpr = append(queryInfo.ScalarFuncExpr, expr)
			}
		}
	case *PhysicalApply:
		last := len(idxs) - 1
		idx := idxs[last]
		children := queryInfos[idx:]
		queryInfos = queryInfos[:idx]
		idxs = idxs[:last]
		queryInfo = multiJoinQueryExprInfo(children)
	case *PhysicalSort:
		sortItems := []*expression.Column{}
		for _, e := range x.ByItems {
			switch expr := e.Expr.(type) {
			case *expression.Column:
				sortItems = append(sortItems, expr)
			}
		}
		queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, sortItems)
	case *PhysicalUnionAll:
		last := len(idxs) - 1
		idx := idxs[last]
		children := queryInfos[idx:]
		queryInfos = queryInfos[:idx]
		queryInfo = multiJoinQueryExprInfo(children)
		idxs = idxs[:last]
	case *PhysicalSelection:
		for _, e := range x.Conditions {
			switch expr := e.(type) {
			case *expression.ScalarFunction:
				queryInfo.ScalarFuncExpr = append(queryInfo.ScalarFuncExpr, expr)
			}
		}
	case *PhysicalProjection:
		queryInfo.ProjExpr = append(queryInfo.ProjExpr, x.Exprs...)
	case *PhysicalTopN:
		byItems := []*expression.Column{}
		for _, e := range x.ByItems {
			switch expr := e.Expr.(type) {
			case *expression.Column:
				byItems = append(byItems, expr)
			}
		}
		queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, byItems)
	case *PhysicalHashAgg:
		for _, af := range x.AggFuncs {
			e := af.Args[0]
			switch expr := e.(type) {
			case *expression.Column:
				queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{expr})
			}
		}
		groupByCols := []*expression.Column{}
		for _, gbyItems := range x.GroupByItems {
			switch expr := gbyItems.(type) {
			case *expression.Column:
				groupByCols = append(groupByCols, expr)
			}
		}
		queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, groupByCols)
	case *PhysicalStreamAgg:
		for _, af := range x.AggFuncs {
			e := af.Args[0]
			switch expr := e.(type) {
			case *expression.Column:
				queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{expr})
			}
		}
		groupByCols := []*expression.Column{}
		for _, gbyItems := range x.GroupByItems {
			switch expr := gbyItems.(type) {
			case *expression.Column:
				groupByCols = append(groupByCols, expr)
			}
		}
		queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, groupByCols)
	case *PhysicalTableReader:
		queryInfo = NewQueryExprInfo(x.tablePlan)
	case *PhysicalIndexReader:
		queryInfo = NewQueryExprInfo(x.indexPlan)
	case *PhysicalIndexLookUpReader:
		queryInfo = doubleJoinQueryExprInfo(NewQueryExprInfo(x.indexPlan), NewQueryExprInfo(x.tablePlan))
	case *PhysicalUnionScan:
		for _, e := range x.Conditions {
			switch x := e.(type) {
			case *expression.ScalarFunction:
				queryInfo.ScalarFuncExpr = append(queryInfo.ScalarFuncExpr, x)
			}
		}
	case *PhysicalIndexJoin:
		last := len(idxs) - 1
		idx := idxs[last]
		children := queryInfos[idx:]
		queryInfos = queryInfos[:idx]
		idxs = idxs[:last]
		queryInfo = multiJoinQueryExprInfo(children)
		for i := range x.OuterJoinKeys {
			l := x.OuterJoinKeys[i]
			r := x.InnerJoinKeys[i]
			queryInfo.ColumnExpr = append(queryInfo.ColumnExpr, []*expression.Column{l}, []*expression.Column{r})
		}
		for _, e := range x.OtherConditions {
			switch expr := e.(type) {
			case *expression.ScalarFunction:
				queryInfo.ScalarFuncExpr = append(queryInfo.ScalarFuncExpr, expr)
			}
		}
	}
	queryInfos = append(queryInfos, queryInfo)
	return queryInfos, idxs
}

func getAllScalarFunc(functions []*expression.ScalarFunction) []*expression.ScalarFunction {
	allScalarFunc := []*expression.ScalarFunction{}
	for _, f := range functions {
		scalarFunc := []*expression.ScalarFunction{}
		recursiveGetScalarFunc(f, &scalarFunc)
		allScalarFunc = append(allScalarFunc, scalarFunc...)
	}
	return allScalarFunc
}

func recursiveGetScalarFunc(f *expression.ScalarFunction, functions *[]*expression.ScalarFunction) {
	switch f.FuncName.L {
	case "or", "and":
		args := f.GetArgs()
		switch e := args[0].(type) {
		case *expression.ScalarFunction:
			recursiveGetScalarFunc(e, functions)
		}
		switch e := args[1].(type) {
		case *expression.ScalarFunction:
			recursiveGetScalarFunc(e, functions)
		}
	default:
		*functions = append(*functions, f)
	}
}

// NewTableInfoSets constructs the table and its sets for forming virtual indices with queryInfo.
func NewTableInfoSets(queryInfo *QueryExprInfo) map[int64]*TableInfoSets {
	tblInfoMap := make(map[int64]*TableInfoSets)
	for _, ds := range queryInfo.Ds {
		meta := ds.Table
		tblInfoMap[meta.ID] = &TableInfoSets{TblInfo: meta}
	}

	// form eq or rg
	queryInfo.ScalarFuncExpr = getAllScalarFunc(queryInfo.ScalarFuncExpr)
	for _, expr := range queryInfo.ScalarFuncExpr {
		var flag string
		switch expr.FuncName.L {
		case "eq", "in":
			flag = EQ
		case "gt", "lt", "ne", "ge", "le":
			flag = RANGE
		}

		args := expr.GetArgs()
		for _, arg := range args {
			switch e := arg.(type) {
			case *expression.Column:
				addToSet(e, &tblInfoMap, flag)
			}
		}
	}

	// form o
	for _, expr := range queryInfo.ColumnExpr {
		tblNameSet := splitColumns(expr)
		for name, set := range tblNameSet {
			addToOSet(name, *set, &tblInfoMap)
		}
	}

	for _, tblInfoSets := range tblInfoMap {
		tblInfoSets.O = removeRepeatedColumnSet(tblInfoSets.O)
	}

	// form ref
	for _, expr := range queryInfo.ProjExpr {
		switch e := expr.(type) {
		case *expression.Column:
			flag := REF
			addToSet(e, &tblInfoMap, flag)
		}
	}

	return tblInfoMap
}

func addToSet(e *expression.Column, tblInfoMap *map[int64]*TableInfoSets, flag string) {
	if e.OrigColName.O == "" {
		return
	}

	colName := e.OrigColName
	tblName := e.OrigTblName
	for _, tblInfoSets := range *tblInfoMap {
		if tblInfoSets.TblInfo.Name.L == tblName.L {
			switch flag {
			case EQ:
				tblInfoSets.Eq = append(tblInfoSets.Eq, colName)
			case RANGE:
				tblInfoSets.Rg = append(tblInfoSets.Rg, colName)
			case REF:
				tblInfoSets.Ref = append(tblInfoSets.Ref, colName)
			}
		}
	}
}

func addToOSet(name string, set []model.CIStr, tblInfoMap *map[int64]*TableInfoSets) {
	for _, tblInfoSets := range *tblInfoMap {
		if tblInfoSets.TblInfo.Name.L == name {
			tblInfoSets.O = append(tblInfoSets.O, set)
		}
	}
}

func splitColumns(columnExpr []*expression.Column) map[string]*[]model.CIStr {
	tblNameSet := make(map[string]*[]model.CIStr)
	for _, expr := range columnExpr {
		if _, is := tblNameSet[expr.OrigTblName.L]; !is {
			tblNameSet[expr.OrigTblName.L] = &[]model.CIStr{expr.OrigColName}
		}
		*tblNameSet[expr.OrigTblName.L] = append(*tblNameSet[expr.OrigTblName.L], expr.OrigColName)
	}

	for _, columns := range tblNameSet {
		*columns = removeRepeatedColumn(*columns)
	}

	return tblNameSet
}

func removeRepeatedColumn(columns []model.CIStr) (ret []model.CIStr) {
	ret = make([]model.CIStr, 0)
	for _, s := range columns {
		if len(ret) == 0 {
			ret = append(ret, s)
		} else {
			for i, v := range ret {
				if reflect.DeepEqual(s, v) {
					break
				}
				if i == len(ret)-1 {
					ret = append(ret, s)
				}
			}
		}
	}
	return
}

func removeRepeatedColumnSet(columnSet [][]model.CIStr) (ret [][]model.CIStr) {
	ret = make([][]model.CIStr, 0)
	for _, s := range columnSet {
		if len(ret) == 0 {
			ret = append(ret, s)
		} else {
			for i, v := range ret {
				if reflect.DeepEqual(s, v) {
					break
				}
				if i == len(ret)-1 {
					ret = append(ret, s)
				}
			}
		}
	}
	return
}
