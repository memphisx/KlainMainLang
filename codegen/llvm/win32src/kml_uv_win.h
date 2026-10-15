// kml_uv_win.h — <uv.h> beside the shim's POSIX surface (TDD-00252).
//
// A runtime file that owns libuv handles and also speaks the shim's
// descriptor-level socket API (kml_posix_compat.h: int descriptors, Linux
// constants) cannot see Winsock's declarations of the same names, which
// <uv.h> brings in. Each such name is renamed while <uv.h> (and so Winsock)
// is read, Winsock's constants are dropped, and the compat header then
// declares the shim's functions under the real names. libuv's own code is
// compiled elsewhere and still calls Winsock.
#ifndef KML_UV_WIN_H
#define KML_UV_WIN_H

#define socket kml_ws_socket
#define bind kml_ws_bind
#define listen kml_ws_listen
#define accept kml_ws_accept
#define connect kml_ws_connect
#define shutdown kml_ws_shutdown
#define setsockopt kml_ws_setsockopt
#define getsockopt kml_ws_getsockopt
#define getsockname kml_ws_getsockname
#define getpeername kml_ws_getpeername
#define recvfrom kml_ws_recvfrom
#define sendto kml_ws_sendto
#define recv kml_ws_recv
#define send kml_ws_send
#define htons kml_ws_htons
#define ntohs kml_ws_ntohs
#define htonl kml_ws_htonl
#define ntohl kml_ws_ntohl
#define inet_pton kml_ws_inet_pton
#define inet_ntop kml_ws_inet_ntop
#define getaddrinfo kml_ws_getaddrinfo
#define freeaddrinfo kml_ws_freeaddrinfo
#define getnameinfo kml_ws_getnameinfo
#define select kml_ws_select
// and the CRT's (io.h, direct.h, sys/stat.h), which the shim redefines
#define read kml_crt_read
#define write kml_crt_write
#define open kml_crt_open
#define close kml_crt_close
#define lseek kml_crt_lseek
#define stat kml_crt_stat
#define fstat kml_crt_fstat
#define mkdir kml_crt_mkdir
#define rmdir kml_crt_rmdir
#define getcwd kml_crt_getcwd
#define chdir kml_crt_chdir
#define unlink kml_crt_unlink
#define access kml_crt_access
#define chmod kml_crt_chmod
#define dup kml_crt_dup
#define dup2 kml_crt_dup2
#define isatty kml_crt_isatty
#define umask kml_crt_umask
#include <uv.h>
#undef read
#undef write
#undef open
#undef close
#undef lseek
#undef stat
#undef fstat
#undef mkdir
#undef rmdir
#undef getcwd
#undef chdir
#undef unlink
#undef access
#undef chmod
#undef dup
#undef dup2
#undef isatty
#undef umask
#undef socket
#undef bind
#undef listen
#undef accept
#undef connect
#undef shutdown
#undef setsockopt
#undef getsockopt
#undef getsockname
#undef getpeername
#undef recvfrom
#undef sendto
#undef recv
#undef send
#undef htons
#undef ntohs
#undef htonl
#undef ntohl
#undef inet_pton
#undef inet_ntop
#undef getaddrinfo
#undef freeaddrinfo
#undef getnameinfo
#undef select

// Winsock's numbers for these differ from Linux's, which the shim takes.
#undef AF_INET
#undef AF_INET6
#undef SOCK_STREAM
#undef SOCK_DGRAM
#undef TCP_NODELAY
#undef SOL_SOCKET
#undef SO_REUSEADDR
#undef SO_KEEPALIVE
#undef SO_ERROR
#undef SO_BROADCAST
#undef SO_SNDBUF
#undef SO_RCVBUF
#undef SHUT_RDWR
#undef SHUT_WR
#undef INADDR_ANY
#undef INADDR_LOOPBACK
#undef EAI_NONAME
#undef EAI_AGAIN
#undef EAI_NODATA
#undef AF_UNIX

#define KML_COMPAT_OVER_WINSOCK 1
#include "kml_posix_compat.h"

#endif
