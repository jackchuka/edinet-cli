// Package codes holds the EDINET code tables from the API specification's
// reference chapter. They change only when the FSA revises the spec, so they
// are compiled in rather than fetched.
package codes

import (
	"fmt"
	"sort"
	"strings"
)

// DocTypes maps 書類種別コード to its Japanese name.
var DocTypes = map[string]string{
	"010": "有価証券通知書",
	"020": "変更通知書（有価証券通知書）",
	"030": "有価証券届出書",
	"040": "訂正有価証券届出書",
	"050": "届出の取下げ願い",
	"060": "発行登録通知書",
	"070": "変更通知書（発行登録通知書）",
	"080": "発行登録書",
	"090": "訂正発行登録書",
	"100": "発行登録追補書類",
	"110": "発行登録取下届出書",
	"120": "有価証券報告書",
	"130": "訂正有価証券報告書",
	"135": "確認書",
	"136": "訂正確認書",
	"140": "四半期報告書",
	"150": "訂正四半期報告書",
	"160": "半期報告書",
	"170": "訂正半期報告書",
	"180": "臨時報告書",
	"190": "訂正臨時報告書",
	"200": "親会社等状況報告書",
	"210": "訂正親会社等状況報告書",
	"220": "自己株券買付状況報告書",
	"230": "訂正自己株券買付状況報告書",
	"235": "内部統制報告書",
	"236": "訂正内部統制報告書",
	"240": "公開買付届出書",
	"250": "訂正公開買付届出書",
	"260": "公開買付撤回届出書",
	"270": "公開買付報告書",
	"280": "訂正公開買付報告書",
	"290": "意見表明報告書",
	"300": "訂正意見表明報告書",
	"310": "対質問回答報告書",
	"320": "訂正対質問回答報告書",
	"330": "別途買付け禁止の特例を受けるための申出書",
	"340": "訂正別途買付け禁止の特例を受けるための申出書",
	"350": "大量保有報告書",
	"360": "訂正大量保有報告書",
	"370": "基準日の届出書",
	"380": "変更の届出書",
}

// Ordinances maps 府令コード to its Japanese name.
var Ordinances = map[string]string{
	"010": "企業内容等の開示に関する内閣府令",
	"015": "財務計算に関する書類その他の情報の適正性を確保するための体制に関する内閣府令",
	"020": "外国債等の発行者の開示に関する内閣府令",
	"030": "特定有価証券の内容等の開示に関する内閣府令",
	"040": "発行者以外の者による株券等の公開買付けの開示に関する内閣府令",
	"050": "発行者による上場株券等の公開買付けの開示に関する内閣府令",
	"060": "株券等の大量保有の状況の開示に関する内閣府令",
}

// aliases give the document types people actually ask for a name that is
// typable on a US keyboard.
var aliases = map[string]string{
	"yuho":         "120",
	"yuho-teisei":  "130",
	"kessan":       "120",
	"shihanki":     "140",
	"hanki":        "160",
	"rinji":        "180",
	"naibu":        "235",
	"kakunin":      "135",
	"taryo":        "350",
	"taryo-teisei": "360",
	"tob":          "240",
	"jiko":         "220",
	"todokede":     "030",
}

// Aliases returns the alias table sorted by alias for display.
func Aliases() [][2]string {
	out := make([][2]string, 0, len(aliases))
	for a, code := range aliases {
		out = append(out, [2]string{a, code})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// ResolveDocType turns a --type value into a 書類種別コード. It accepts either an
// alias ("yuho") or a raw code ("120").
func ResolveDocType(s string) (string, error) {
	s = strings.TrimSpace(s)
	if code, ok := aliases[strings.ToLower(s)]; ok {
		return code, nil
	}
	if _, ok := DocTypes[s]; ok {
		return s, nil
	}
	return "", fmt.Errorf("unknown document type %q: pass a code like 120 or an alias like yuho (see `edinet codes`)", s)
}

// DocTypeName returns the Japanese name for a code, or the code itself when the
// FSA has added one this build does not know about.
func DocTypeName(code string) string {
	if name, ok := DocTypes[code]; ok {
		return name
	}
	return code
}

// SortedDocTypes returns doc types ordered by code, for display.
func SortedDocTypes() [][2]string {
	return sortedPairs(DocTypes)
}

// SortedOrdinances returns ordinances ordered by code, for display.
func SortedOrdinances() [][2]string {
	return sortedPairs(Ordinances)
}

func sortedPairs(m map[string]string) [][2]string {
	out := make([][2]string, 0, len(m))
	for k, v := range m {
		out = append(out, [2]string{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
