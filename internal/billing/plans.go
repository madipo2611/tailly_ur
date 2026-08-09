package billing

import "time"

type Plan struct {
	Code, Name, Description string
	MonthlyPriceKopecks     int64
	DocumentsPerMonth       int
}

func Plans() []Plan {
	return []Plan{{Code: "start", Name: "Старт", Description: "Для небольших команд", MonthlyPriceKopecks: 99000, DocumentsPerMonth: 50}, {Code: "business", Name: "Бизнес", Description: "Для компаний с регулярными сделками", MonthlyPriceKopecks: 399000, DocumentsPerMonth: 500}, {Code: "enterprise", Name: "Корпоративный", Description: "Индивидуальные лимиты и интеграции", MonthlyPriceKopecks: 0, DocumentsPerMonth: 0}}
}

type Subscription struct {
	PlanCode, Status    string
	DocumentsUsed       int
	CurrentPeriodEndsAt time.Time
}
type SubscriptionUpdate struct {
	Phone, PlanCode, Status string
	CurrentPeriodEndsAt     time.Time
}

func DocumentLimit(code string) int {
	for _, p := range Plans() {
		if p.Code == code {
			return p.DocumentsPerMonth
		}
	}
	return 0
}
func ValidStatus(status string) bool {
	switch status {
	case "active", "past_due", "cancelled", "trial":
		return true
	}
	return false
}
