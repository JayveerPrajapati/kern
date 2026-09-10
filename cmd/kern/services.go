package main

import (
	"github.com/JayveerPrajapati/kern/internal/service"
)

// svc is the shared service layer for the CLI. Commands delegate their
// business logic to the delivery-mechanism-independent service layer
// (internal/service) instead of reaching into the internal engines directly,
// so the same operations are available to the MCP server and the web console.
var svc = service.New()
