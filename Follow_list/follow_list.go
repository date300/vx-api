package Follow_list

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"vx-api/Config"
	"vx-api/Middleware"
	"vx-api/Profile"

	"github.com/gin-gonic/gin"
)

// Handlers moved to Profile package to avoid circular dependency
// and consolidate /user routes.
