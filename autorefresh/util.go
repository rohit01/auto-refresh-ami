package autorefresh

func CopyMap(source *map[string]string) map[string]string {
	newMap := make(map[string]string)
	for k, v := range *source {
		newMap[k] = v
	}
	return newMap
}
