// Package doorphoneserver implementa il client radio PTT basato su Mumble con supporto
// per periferiche GPIO, MQTT, HTTP API e integrazione con dispositivi hardware.
package doorphoneserver

import (
	"context"
	"sync"
	"time"
)

// userConnectLog tracks the connection timestamp for each Mumble session.
var (
	userConnectLog   = map[uint32]time.Time{}
	userConnectMutex sync.RWMutex
)

// SetUserConnected records when a user session connected.
func SetUserConnected(session uint32) {
	userConnectMutex.Lock()
	defer userConnectMutex.Unlock()
	userConnectLog[session] = time.Now()
}

// RemoveUserConnected removes the connection record for a session.
func RemoveUserConnected(session uint32) {
	userConnectMutex.Lock()
	defer userConnectMutex.Unlock()
	delete(userConnectLog, session)
}

// GetUserConnectedAt returns the connection time for a session, or zero time if not found.
func GetUserConnectedAt(session uint32) time.Time {
	userConnectMutex.RLock()
	defer userConnectMutex.RUnlock()
	return userConnectLog[session]
}

var (
	// globalCtx è il contesto globale dell'applicazione, usato per la cancellazione coordinata
	globalCtx    context.Context
	// globalCancel è la funzione di cancellazione associata al contesto globale
	globalCancel context.CancelFunc
	// ctxMutex protegge l'accesso concorrente al contesto globale
	ctxMutex     sync.RWMutex
)

// SetGlobalContext imposta il contesto globale per il package doorphoneserver
func SetGlobalContext(ctx context.Context, cancel context.CancelFunc) {
	ctxMutex.Lock()
	defer ctxMutex.Unlock()
	globalCtx = ctx
	globalCancel = cancel
}

// GetGlobalContext restituisce il contesto globale
func GetGlobalContext() context.Context {
	ctxMutex.RLock()
	defer ctxMutex.RUnlock()
	if globalCtx == nil {
		return context.Background()
	}
	return globalCtx
}

// CancelGlobalContext cancella il contesto globale per lo shutdown
func CancelGlobalContext() {
	ctxMutex.RLock()
	defer ctxMutex.RUnlock()
	if globalCancel != nil {
		globalCancel()
	}
}