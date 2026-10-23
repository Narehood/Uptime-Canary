package jsonquery

import (
	"context"
	"testing"
)

func TestR3PrivateNativeNamesNotBindings(t *testing.T) {
	supported(t)
	for _, expression := range []string{
		"$canaryPrivateValidator(1)", "$canaryCapturedValidator(1)",
		"$canaryFinite(1)", "$privateValidator(1)",
		"$eval('$canaryPrivateValidator(1)')",
	} {
		if got := Check(context.Background(), request(expression, "1")); got.State != "down" {
			t.Fatalf("private validator became a JSONata binding: %+v", got)
		}
	}
}
