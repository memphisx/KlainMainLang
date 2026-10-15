#!/bin/sh
# Watches a Windows test run: logs prog.exe count and free memory every 20s;
# kills the run (prog.exe trees, then testshard and test binaries) when free
# memory drops below 6 GB or prog.exe count passes 400. Stops when no
# $IMG (go test's binary) runs. $1 is the log file.
LOG=$1; IMG=${2:-tests.test.exe}
until tasklist //FI "IMAGENAME eq $IMG" //NH | grep -qi "$IMG"; do sleep 2; done
while tasklist //FI "IMAGENAME eq $IMG" //NH | grep -qi "$IMG"; do
  n=$(tasklist //FI "IMAGENAME eq prog.exe" //NH 2>/dev/null | grep -c prog.exe)
  free=$(powershell -NoProfile -Command "[int]((Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory/1KB)")
  echo "$(date +%T) prog=$n freeMB=$free" >> "$LOG"
  if [ "$n" -gt 400 ] || [ "$free" -lt 6000 ]; then
    echo "$(date +%T) LIMIT HIT - killing run" >> "$LOG"
    taskkill //F //IM prog.exe //T >/dev/null 2>&1
    taskkill //F //IM $IMG //T >/dev/null 2>&1
    taskkill //F //IM prog.exe //T >/dev/null 2>&1
    exit 1
  fi
  sleep 20
done
