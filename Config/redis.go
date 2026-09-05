package Config

import (
	"context"
	"fmt"

	"github.com/go-redis/redis/v8"
)

var RedisClient *redis.Client
var Ctx = context.Background()

func InitRedis() {
	fmt.Println("REDIS: Skipping initialization for stability...")
	RedisClient = nil
}
