#!/bin/sh
set -eu

repository_root=$(
  CDPATH= cd -- "$(dirname -- "$0")/.." &&
    pwd
)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/stacks-env-file-test.XXXXXX")

cleanup() {
  test -n "$test_root"
  rm -rf -- "$test_root"
}
trap cleanup EXIT HUP INT TERM

run_case() {
  shell_path=$1
  case_name=$2
  path_kind=$3
  case_root="$test_root/$case_name"

  mkdir -p "$case_root/bin"
  case_root_path=$(cd "$case_root" && pwd -P)
  case "$path_kind" in
    basename)
      env_file_value=.env.basename
      env_file_path="$case_root_path/$env_file_value"
      ;;
    relative-spaces)
      env_file_value='config directory/.env with spaces'
      env_file_path="$case_root_path/$env_file_value"
      ;;
    relative-literal-syntax)
      env_file_value='literal $(not-a-command)/.env with spaces'
      env_file_path="$case_root_path/$env_file_value"
      ;;
    absolute-spaces)
      env_file_value="$case_root_path/absolute directory/.env with spaces"
      env_file_path=$env_file_value
      ;;
    *)
      echo "unknown environment file path kind" >&2
      exit 1
      ;;
  esac
  go_invocation_marker="$case_root/go-invoked"
  docker_invocation_marker="$case_root/docker-invoked"

  mkdir -p "$(dirname -- "$env_file_path")"
  cat >"$env_file_path" <<'EOF'
STACKS_ENV_FILE_TEST_MARKER=loaded
STACKS_ENV_FILE_TEST_SOURCE_COUNT=$(( ${STACKS_ENV_FILE_TEST_SOURCE_COUNT:-0} + 1 ))
EOF
  cat >"$case_root/bin/go" <<'EOF'
#!/bin/sh
set -eu

if test "${STACKS_MAKE_ENV_FILE:-}" != "$STACKS_ENV_FILE_TEST_EXPECTED_INPUT"; then
  echo "environment file path was not transported as one value" >&2
  exit 1
fi
if test "${STACKS_ENV_FILE_TEST_MARKER:-}" != loaded; then
  echo "environment file was not sourced" >&2
  exit 1
fi
if test "${STACKS_ENV_FILE_TEST_SOURCE_COUNT:-}" != 1; then
  echo "environment file was not sourced exactly once" >&2
  exit 1
fi
if test "$#" -ne 3 ||
  test "$1" != run ||
  test "$2" != ./cmd/stacks ||
  test "$3" != db-migrate; then
  echo "unexpected db-migrate command" >&2
  exit 1
fi

touch "$STACKS_ENV_FILE_TEST_INVOCATION_MARKER"
EOF
  chmod +x "$case_root/bin/go"

  cat >"$case_root/bin/docker" <<'EOF'
#!/bin/sh
set -eu

if test "${STACKS_MAKE_ENV_FILE:-}" != "$STACKS_ENV_FILE_TEST_EXPECTED_INPUT"; then
  echo "environment file path was not transported as one value" >&2
  exit 1
fi
if test "$#" -ne 7 ||
  test "$1" != compose ||
  test "$2" != --env-file ||
  test "$4" != up ||
  test "$5" != --detach ||
  test "$6" != --wait ||
  test "$7" != postgres; then
  echo "unexpected db-up command" >&2
  exit 1
fi
case "$3" in
  /*) ;;
  *)
    echo "db-up did not receive an absolute environment file path" >&2
    exit 1
    ;;
esac
received_env_file_path=$(
  CDPATH= cd -- "$(dirname -- "$3")" &&
    printf '%s/%s\n' "$(pwd -P)" "$(basename -- "$3")"
)
if test "$received_env_file_path" != "$STACKS_ENV_FILE_TEST_EXPECTED_PATH"; then
  echo "db-up did not receive the resolved environment file path" >&2
  exit 1
fi

touch "$STACKS_ENV_FILE_TEST_INVOCATION_MARKER"
EOF
  chmod +x "$case_root/bin/docker"

  rendered_recipe=$(
    cd "$case_root"
    make --no-print-directory --dry-run -f "$repository_root/Makefile" \
      SHELL="$shell_path" ENV_FILE="$env_file_value" db-migrate
  )
  expected_file_check='test -f "$env_file_path"'
  expected_source_path='. "$env_file_path"'
  case "$rendered_recipe" in
    *"$expected_file_check"*"$expected_source_path"*) ;;
    *)
      echo "db-migrate did not render canonical environment file checks" >&2
      exit 1
      ;;
  esac

  rendered_compose_recipe=$(
    cd "$case_root"
    make --no-print-directory --dry-run -f "$repository_root/Makefile" \
      SHELL="$shell_path" ENV_FILE="$env_file_value" db-up
  )
  expected_compose_path='docker compose --env-file "$env_file_path"'
  case "$rendered_compose_recipe" in
    *"$expected_file_check"*"$expected_compose_path"*) ;;
    *)
      echo "db-up did not render the canonical environment file path" >&2
      exit 1
      ;;
  esac

  (
    cd "$case_root"
    PATH="$case_root/bin:$PATH" \
      STACKS_ENV_FILE_TEST_MARKER= \
      STACKS_ENV_FILE_TEST_SOURCE_COUNT= \
      STACKS_ENV_FILE_TEST_EXPECTED_INPUT="$env_file_value" \
      STACKS_ENV_FILE_TEST_EXPECTED_PATH="$env_file_path" \
      STACKS_ENV_FILE_TEST_INVOCATION_MARKER="$go_invocation_marker" \
      make --no-print-directory -s -f "$repository_root/Makefile" \
      SHELL="$shell_path" ENV_FILE="$env_file_value" db-migrate
  )

  if ! test -f "$go_invocation_marker"; then
    echo "db-migrate command was not invoked" >&2
    exit 1
  fi

  (
    cd "$case_root"
    PATH="$case_root/bin:$PATH" \
      STACKS_ENV_FILE_TEST_EXPECTED_INPUT="$env_file_value" \
      STACKS_ENV_FILE_TEST_EXPECTED_PATH="$env_file_path" \
      STACKS_ENV_FILE_TEST_INVOCATION_MARKER="$docker_invocation_marker" \
      make --no-print-directory -s -f "$repository_root/Makefile" \
      SHELL="$shell_path" ENV_FILE="$env_file_value" db-up
  )

  if ! test -f "$docker_invocation_marker"; then
    echo "db-up command was not invoked" >&2
    exit 1
  fi
}

run_case /bin/sh system-sh-basename basename
run_case /bin/sh system-sh-relative-spaces relative-spaces
run_case /bin/sh system-sh-relative-literal-syntax relative-literal-syntax
run_case /bin/sh system-sh-absolute-spaces absolute-spaces
if test -x /bin/dash; then
  run_case /bin/dash dash-basename basename
  run_case /bin/dash dash-relative-spaces relative-spaces
  run_case /bin/dash dash-relative-literal-syntax relative-literal-syntax
  run_case /bin/dash dash-absolute-spaces absolute-spaces
fi
