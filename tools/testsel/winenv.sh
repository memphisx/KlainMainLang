# Source from Git Bash on the Windows box: clang + MSYS coreutils on PATH,
# all scratch on the R: RAM drive. Without clang the E2E tests skip silently.
export PATH="/c/msys64/ucrt64/bin:/c/msys64/usr/bin:$PATH"
export GOTMPDIR='R:\kml-scratch' KML_SCRATCH='R:\kml-scratch' TMP='R:\kml-scratch' TEMP='R:\kml-scratch'
export HOME="$USERPROFILE"
