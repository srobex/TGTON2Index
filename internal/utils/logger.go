package utils

import "go.uber.org/zap"

// Создаёт базовый логгер; будет расширен позднее.
func NewLogger() (*zap.Logger, error) {
	return zap.NewDevelopment()
}




