package application

// NormalizeLimit applies the service's bounded default page size.
func NormalizeLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}
