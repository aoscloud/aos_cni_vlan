package main_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestAosVlan(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AosVlan Suite")
}
