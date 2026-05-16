#!/bin/bash
# Riavvia il tablet Android togliendo e ripristinando l'alimentazione via relay GPIO.
# Chiama l'HTTP API del doorphoneserver locale (porta 8080).
# Usato dal crontab per il riavvio programmato del tablet.

API="http://127.0.0.1:8080"

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Spegnimento tablet..."
curl -s -X POST "$API/panel/api/tablet" -d "action=off" > /dev/null

sleep 10

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Accensione tablet..."
curl -s -X POST "$API/panel/api/tablet" -d "action=on" > /dev/null

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Riavvio tablet completato."
