/* entrypath.c — require.main.filename, the module a self-fork runs
 * (lib/native.d.ts). hooks.c holds __kml_entry_path, which the program's
 * constructor sets to its entry file when it calls this. */
extern const char *__kml_entry_path;

const char *__kml_native_entry_path(void) { return __kml_entry_path; }
