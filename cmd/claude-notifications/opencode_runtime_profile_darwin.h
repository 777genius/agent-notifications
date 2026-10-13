#ifndef AN_RUNTIME_DARWIN_H
#define AN_RUNTIME_DARWIN_H
#include <stdint.h>
#include <stddef.h>
// These are shim outputs, never guessed layouts of a kernel ABI structure.
struct an_runtime_process {
 uint32_t pid, parent, cpu, subtype;
 uint64_t seconds, micros;
};
struct an_runtime_region { uint64_t address, size, device, inode; uint32_t executable; };
int an_runtime_process(int pid, struct an_runtime_process *out, char *path, size_t capacity);
int an_runtime_region(int pid, uint64_t address, struct an_runtime_region *out);
int an_runtime_args(int pid, unsigned char *out, size_t capacity, size_t *length);
int an_runtime_watch(int pid);
int an_runtime_unchanged(int fd);
#endif
