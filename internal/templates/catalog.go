package templates

type Template struct {
	Code, Name, Version, Description string
	Fields                           []string
}

func Catalog() []Template {
	return []Template{{Code: "gph-contract", Name: "Договор ГПХ", Version: "1.0", Description: "Договор оказания услуг с самозанятым", Fields: []string{"customer", "contractor", "subject", "amount", "deadline"}}, {Code: "act", Name: "Акт выполненных работ", Version: "1.0", Description: "Подтверждение оказанных услуг", Fields: []string{"customer", "contractor", "services", "amount", "period"}}, {Code: "nda", Name: "NDA", Version: "1.0", Description: "Соглашение о конфиденциальности", Fields: []string{"partyA", "partyB", "confidentialInformation", "term"}}, {Code: "guarantee", Name: "Гарантийное письмо", Version: "1.0", Description: "Обязательство стороны", Fields: []string{"sender", "recipient", "obligation", "deadline"}}}
}
func ByCode(code string) (Template, bool) {
	for _, t := range Catalog() {
		if t.Code == code {
			return t, true
		}
	}
	return Template{}, false
}
