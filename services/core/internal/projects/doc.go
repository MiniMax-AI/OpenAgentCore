// Package projects owns Core Projects and their API keys. A Project is one
// tenant with its own execution scope and assets; its API keys authenticate the
// public API as that Project. Archiving a Project revokes its keys and keeps its
// assets.
package projects
