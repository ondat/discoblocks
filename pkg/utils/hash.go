package utils

import "hash/fnv"

// Hash calculates has of the given string
func Hash(s string) (uint32, error) {
	h := fnv.New32a()
	_, err := h.Write([]byte(s))

	return h.Sum32(), err
}
