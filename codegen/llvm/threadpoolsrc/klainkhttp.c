// ---- klain:http's cluster (TDD-00255) ------------------------------------------
// listen(port, handler, { workers: N }) runs N processes on the one port: on
// POSIX the primary forks N-1 workers after it bound the listener (each
// inherits the socket and accepts on it); on Windows it re-spawns its own
// executable N-1 times with its listening sockets inherited (win32proc.c), a
// worker taking the inherited listener bound to its port instead of binding,
// and the primary dealing connections round-robin (win32io.c). A page of two
// words shared by every process carries http.close() ([0]) and
// closeAllConnections() ([1]) to the siblings, which poll it.

extern void *__kml_cluster_close_flag;
extern int __kml_cluster_primary_pid;
extern void __kml_loop_after_fork(void);

#ifdef _WIN32
extern int __kml_win_spawn_worker(const char *exe, char **argv);
extern char *__kml_win_self_exe(void);
extern void *__kml_win_cluster_flag(void);
extern void *__argv_ptr;
#else
#include <sys/mman.h>
#endif

static double khttp_worker_id;

// Start the workers; this process's worker id (0 in the primary).
double __kml_native_khttp_fork(double workers) {
    int n = (int)workers;
#ifdef _WIN32
    const char *env = getenv("KML_CLUSTER_WORKER_ID");
    if (env) {
        // A re-spawned worker: its id from the environment, the primary's
        // flag page mapped.
        void *mm = __kml_win_cluster_flag();
        if (mm != (void *)(intptr_t)-1) __kml_cluster_close_flag = mm;
        khttp_worker_id = atoi(env);
        return khttp_worker_id;
    }
    if (n <= 1) return 0;
    void *mm = __kml_win_cluster_flag();
    if (mm != (void *)(intptr_t)-1) __kml_cluster_close_flag = mm;
    char *exe = __kml_win_self_exe();
    for (int i = 1; i < n; i++) {
        char id[24];
        snprintf(id, sizeof id, "%d", i);
        fflush(NULL);
        setenv("KML_CLUSTER_WORKER_ID", id, 1);
        __kml_win_spawn_worker(exe, (char **)__argv_ptr);
        unsetenv("KML_CLUSTER_WORKER_ID");
    }
    return 0;
#else
    if (n <= 1) return 0;
    int64_t *mm = (int64_t *)mmap(NULL, 16, PROT_READ | PROT_WRITE, MAP_SHARED | MAP_ANON, -1, 0);
    if (mm != MAP_FAILED) {
        mm[0] = mm[1] = 0;
        __kml_cluster_close_flag = mm;
    }
    for (int i = 1; i < n; i++) {
        // stdio buffers are copied by fork: flushed first, or each worker
        // writes the primary's pending output again.
        fflush(NULL);
        int primary = getpid();
        int pid = fork();
        if (pid == 0) {
            __kml_loop_after_fork();
            __kml_cluster_primary_pid = primary;
            khttp_worker_id = i;
            return i;
        }
    }
    return 0;
#endif
}

// A shared flag: 1 when a sibling set it; 0 outside a cluster. which 2:
// whether this worker's primary is gone (it then exits, as a forked worker
// of a killed primary must).
double __kml_native_khttp_flag(double which) {
    int64_t *flag = (int64_t *)__kml_cluster_close_flag;
    int w = (int)which;
#ifndef _WIN32
    if (w == 2) return __kml_cluster_primary_pid != 0 && getppid() != __kml_cluster_primary_pid;
#endif
    if (!flag || w < 0 || w > 1) return 0;
    return __atomic_load_n(&flag[w], __ATOMIC_SEQ_CST) != 0;
}

void __kml_native_khttp_set_flag(double which) {
    int64_t *flag = (int64_t *)__kml_cluster_close_flag;
    int w = (int)which;
    if (flag && w >= 0 && w <= 1) __atomic_store_n(&flag[w], 1, __ATOMIC_SEQ_CST);
}

// Whether this process is part of a cluster (the flag page exists).
double __kml_native_khttp_clustered(void) { return __kml_cluster_close_flag != 0; }
