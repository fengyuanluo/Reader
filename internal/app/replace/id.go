package replaceapp

import "time"

func nextRuleID() int64 {
	return time.Now().UnixNano()
}
