package exporter

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap   = regexp.MustCompile("([a-z0-9])([A-Z])")
)

func toSnakeCase(str string) string {
	snake := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	snake = matchAllCap.ReplaceAllString(snake, "${1}_${2}")
	return strings.ToLower(snake)
}

func hexToDecimal(hex string) (float64, error) {
	s := hex
	s = strings.Replace(s, "0x", "", -1)
	s = strings.Replace(s, "0X", "", -1)
	parsed, err := strconv.ParseUint(s, 16, 64)
	return float64(parsed), err
}
