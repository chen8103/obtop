package logx

import (
	"log"
	"os"
)

func New() *log.Logger {
	return log.New(os.Stderr, "obtop: ", log.LstdFlags|log.Lmsgprefix)
}
