package fw

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTraceRoutes_GoGin(t *testing.T) {
	dir := t.TempDir()

	goCode := `package main

import (
	"github.com/gin-gonic/gin"
)

func AuthMiddleware(c *gin.Context) {}
func RateLimit(c *gin.Context) {}

func GetUserHandler(c *gin.Context) {
	UserService.FindUser()
}

func main() {
	r := gin.Default()
	r.GET("/api/v1/users/:id", AuthMiddleware, RateLimit, GetUserHandler)
	r.POST("/api/v1/users", AuthMiddleware, CreateUserHandler)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goCode), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	res, err := TraceRoutes(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("TraceRoutes failed: %v", err)
	}

	if res.Total != 2 {
		t.Fatalf("got %d routes, want 2", res.Total)
	}

	var getRoute, postRoute *RouteFlow
	for i := range res.Routes {
		r := &res.Routes[i]
		if r.Method == "GET" && r.Path == "/api/v1/users/:id" {
			getRoute = r
		} else if r.Method == "POST" && r.Path == "/api/v1/users" {
			postRoute = r
		}
	}

	if getRoute == nil {
		t.Fatalf("expected GET /api/v1/users/:id route, got routes: %+v", res.Routes)
	}
	if len(getRoute.Middleware) != 2 || getRoute.Middleware[0] != "AuthMiddleware" || getRoute.Middleware[1] != "RateLimit" {
		t.Errorf("getRoute middlewares = %+v", getRoute.Middleware)
	}
	if getRoute.Handler != "GetUserHandler" {
		t.Errorf("getRoute handler = %s, want GetUserHandler", getRoute.Handler)
	}

	if postRoute == nil {
		t.Fatalf("expected POST /api/v1/users route")
	}
	if len(postRoute.Middleware) != 1 || postRoute.Middleware[0] != "AuthMiddleware" {
		t.Errorf("postRoute middlewares = %+v", postRoute.Middleware)
	}
	if postRoute.Handler != "CreateUserHandler" {
		t.Errorf("postRoute handler = %s, want CreateUserHandler", postRoute.Handler)
	}
}

func TestTraceRoutes_TypeScriptExpressAndNest(t *testing.T) {
	dir := t.TempDir()

	// 1. Express file
	expressCode := `
import express from 'express';
const app = express();
app.get('/api/health', checkAuth, handleHealth);
app.post('/api/items', checkAuth, rateLimiter, createItem);
`
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(expressCode), 0644); err != nil {
		t.Fatalf("write app.ts: %v", err)
	}

	// 2. NestJS file
	nestCode := `
import { Controller, Get, Post } from '@nestjs/common';

@Controller('users')
export class UsersController {
  constructor(private readonly userService: UserService) {}

  @Get(':id')
  async findOne() {
    return this.userService.findById();
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "users.controller.ts"), []byte(nestCode), 0644); err != nil {
		t.Fatalf("write users.controller.ts: %v", err)
	}

	res, err := TraceRoutes(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("TraceRoutes: %v", err)
	}

	if res.Total != 3 {
		t.Fatalf("got %d routes, want 3", res.Total)
	}

	// Check NestJS route
	var nestFound bool
	for _, r := range res.Routes {
		if r.Framework == "nestjs" {
			nestFound = true
			if r.Method != "GET" || r.Path != "/:id" || r.Handler != "findOne" {
				t.Errorf("nest route = %+v", r)
			}
			if len(r.InjectedServices) == 0 {
				t.Errorf("expected injected service in nest route: %+v", r.InjectedServices)
			}
		}
	}
	if !nestFound {
		t.Errorf("expected nestjs route to be detected")
	}
}

func TestTraceRoutes_FastAPI(t *testing.T) {
	dir := t.TempDir()

	pyCode := `
from fastapi import FastAPI, Depends

app = FastAPI()

@app.get("/items/{item_id}")
async def read_item(item_id: int, db: Session = Depends(get_db)):
    return {"item_id": item_id}
`
	if err := os.WriteFile(filepath.Join(dir, "api.py"), []byte(pyCode), 0644); err != nil {
		t.Fatalf("write api.py: %v", err)
	}

	res, err := TraceRoutes(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("TraceRoutes: %v", err)
	}

	if res.Total != 1 {
		t.Fatalf("got %d routes, want 1", res.Total)
	}

	r := res.Routes[0]
	if r.Method != "GET" || r.Path != "/items/{item_id}" || r.Handler != "read_item" {
		t.Errorf("py route = %+v", r)
	}
	if len(r.InjectedServices) != 1 || r.InjectedServices[0] != "get_db" {
		t.Errorf("py injected services = %+v", r.InjectedServices)
	}
}
