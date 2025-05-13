package core

import (
	"fmt"
	"github.com/banbox/banexg/utils"
	"regexp"
	"slices"
	"strings"
)

/*
GroupByPairQuotes
format `[key]:pairs...` as below
【key】
Quote: Base1 Base2 ...
*/
func GroupByPairQuotes(items map[string][]string, doSort bool) string {
	res := make(map[string]map[string][]string)
	for key, arr := range items {
		if doSort {
			slices.Sort(arr)
		}
		quoteMap := make(map[string][]string)
		for _, pair := range arr {
			baseCode, quoteCode, _, _ := SplitSymbol(pair)
			baseList, _ := quoteMap[quoteCode]
			quoteMap[quoteCode] = append(baseList, baseCode)
		}
		for quote, baseList := range quoteMap {
			if doSort {
				slices.Sort(baseList)
			}
			quoteMap[quote] = baseList
		}
		res[key] = quoteMap
	}
	var b strings.Builder
	for key, quoteMap := range res {
		b.WriteString(fmt.Sprintf("【%s】\n", key))
		for quoteCode, arr := range quoteMap {
			baseStr := strings.Join(arr, " ")
			b.WriteString(fmt.Sprintf("%s(%d): %s\n", quoteCode, len(arr), baseStr))
		}
	}
	return b.String()
}

var (
	reCoinSplit = regexp.MustCompile("[/:-]")
	splitCache  = map[string][4]string{}
)

/*
SplitSymbol
return Base，Quote，Settle，Identifier
*/
func SplitSymbol(pair string) (string, string, string, string) {
	if cache, ok := splitCache[pair]; ok {
		return cache[0], cache[1], cache[2], cache[3]
	}
	if ExgName == "china" {
		parts := utils.SplitParts(pair)
		code := parts[0].Val
		yearMon := parts[1].Val
		splitCache[pair] = [4]string{code, "CNY", "CNY", yearMon}
	} else {
		parts := reCoinSplit.Split(pair, -1)
		settle, ident := "", ""
		if len(parts) > 2 {
			settle = parts[2]
			if len(parts) > 3 {
				ident = parts[3]
			}
		} else if len(parts) < 2 {
			return parts[0], "", "", ""
		}
		splitCache[pair] = [4]string{parts[0], parts[1], settle, ident}
	}
	cache, _ := splitCache[pair]
	return cache[0], cache[1], cache[2], cache[3]
}
