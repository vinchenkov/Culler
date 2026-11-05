#include <dlfcn.h>
#include <errno.h>
#include <libproc.h>
#include <limits.h>
#include <mach/mach.h>
#include <mach/task_policy.h>
#include <spawn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

extern char **environ;

typedef int (*set_process_type_fn)(const posix_spawnattr_t *, int);

/* Apple defines these process types in bsd/sys/spawn_internal.h. */
enum { PROCESS_TYPE_STANDARD = 0x300, PROCESS_TYPE_BACKGROUND = 0x500 };

static int fail(const char *operation, int error) {
  fprintf(stderr, "{\"error\":\"%s\",\"code\":%d}\n", operation, error);
  return 125;
}

static int report(pid_t pid, int report_self) {
  struct proc_taskinfo info = {0};
  errno = 0;
  int size = proc_pidinfo(pid, PROC_PIDTASKINFO, 0, &info, sizeof(info));
  if (size != sizeof(info)) {
    return fail("read_task_priority", errno != 0 ? errno : EIO);
  }

  if (report_self) {
    task_category_policy_data_t category = {0};
    mach_msg_type_number_t count = TASK_CATEGORY_POLICY_COUNT;
    boolean_t get_default = FALSE;
    kern_return_t result = task_policy_get(
        mach_task_self(), TASK_CATEGORY_POLICY, (task_policy_t)&category,
        &count, &get_default);
    if (result != KERN_SUCCESS || get_default) {
      return fail("read_task_category", (int)result);
    }
    printf("{\"pid\":%d,\"parent_pid\":%d,\"task_priority\":%d,"
           "\"task_role\":%d}\n",
           (int)pid, (int)getppid(), info.pti_priority, category.role);
  } else {
    printf("{\"pid\":%d,\"task_priority\":%d}\n", (int)pid,
           info.pti_priority);
  }
  return 0;
}

int main(int argc, char **argv) {
  if (argc == 2 && strcmp(argv[1], "--report-self") == 0) {
    return report(getpid(), 1);
  }
  if (argc == 3 && strcmp(argv[1], "--report") == 0) {
    char *end = NULL;
    errno = 0;
    long value = strtol(argv[2], &end, 10);
    if (errno != 0 || end == argv[2] || *end != '\0' || value <= 0 ||
        value > INT_MAX) {
      return fail("invalid_process_identifier", EINVAL);
    }
    return report((pid_t)value, 0);
  }
  if (argc < 3) {
    return fail("expected_mode_and_executable", EINVAL);
  }

  int process_type;
  if (strcmp(argv[1], "standard") == 0) {
    process_type = PROCESS_TYPE_STANDARD;
  } else if (strcmp(argv[1], "background") == 0) {
    process_type = PROCESS_TYPE_BACKGROUND;
  } else {
    return fail("invalid_process_type", EINVAL);
  }
  if (argv[2][0] != '/') {
    return fail("expected_absolute_executable_path", EINVAL);
  }

  set_process_type_fn set_process_type =
      (set_process_type_fn)dlsym(RTLD_DEFAULT,
                               "posix_spawnattr_setprocesstype_np");
  if (set_process_type == NULL) {
    return fail("process_type_api_unavailable", ENOSYS);
  }

  posix_spawnattr_t attributes;
  int result = posix_spawnattr_init(&attributes);
  if (result != 0) {
    return fail("initialize_spawn_attributes", result);
  }
  result = set_process_type(&attributes, process_type);
  if (result == 0) {
    result = posix_spawnattr_setflags(&attributes, POSIX_SPAWN_SETEXEC);
  }
  if (result != 0) {
    posix_spawnattr_destroy(&attributes);
    return fail("set_process_type", result);
  }

  pid_t pid = 0;
  result = posix_spawn(&pid, argv[2], NULL, &attributes, &argv[2], environ);
  posix_spawnattr_destroy(&attributes);
  return fail("execute_process", result != 0 ? result : EIO);
}
