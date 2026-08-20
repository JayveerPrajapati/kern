// Package data extracts database schemas and ORM models for the Digital
// Twin's Data category. It scans SQL migrations for CREATE TABLE and ORM
// definitions (GORM, SQLAlchemy, JPA), producing domain.Database and
// domain.Table nodes. Deterministic — no LLM.
package data
