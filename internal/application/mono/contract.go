package mono

import (
	"fmt"
	"math"
	"strings"
)

func SignedAmount(amount int64, direction string) (int64, error) {
	switch strings.ToLower(direction) {
	case "debit":
		if amount > 0 {
			return -amount, nil
		}
	case "credit":
		if amount == math.MinInt64 {
			return 0, fmt.Errorf("transaction amount overflow")
		}
		if amount < 0 {
			return -amount, nil
		}
	}
	return amount, nil
}
func BankName(code string) string {
	names := map[string]string{"058": "Guaranty Trust Bank", "044": "Access Bank", "063": "Access Bank", "011": "First Bank of Nigeria", "033": "United Bank for Africa", "057": "Zenith Bank", "035": "Wema Bank", "232": "Sterling Bank", "070": "Fidelity Bank", "214": "First City Monument Bank", "050": "Ecobank", "221": "Stanbic IBTC Bank", "032": "Union Bank", "082": "Keystone Bank", "076": "Polaris Bank", "090405": "Moniepoint", "100004": "OPay", "999992": "OPay", "100003": "PalmPay", "090267": "Kuda Bank"}
	if name := names[code]; name != "" {
		return name
	}
	return "Linked bank"
}
