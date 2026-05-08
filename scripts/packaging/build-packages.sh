#!/usr/bin/env bash
# build-packages.sh - Build cairo2 as .deb and .rpm packages.
#
# Produces three packages per format:
#   cairo          - main binary + web-agent + vscode extension + systemd user units
#   cairo-registry - registry daemon binary + systemd system unit
#   cairo-ctl      - CLI control binary only
#
# All artifacts land in dist/.
#
# Usage:
#   bash scripts/packaging/build-packages.sh [options]
#
# Options:
#   --version VERSION   Set package version (default: auto from git)
#   --deb               Build .deb only
#   --rpm               Build .rpm only
#   --all               Build both when tools are available (default)
#   --skip-extension    Do not build/include the VS Code extension
#   --skip-web-agent    Do not build/include the browser web agent
#   --skip-tests        Skip pre-package lint/test gate
#   --help, -h          Show this help

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
EXT_DIR="$REPO_ROOT/vscode-extension"
WEB_AGENT_STAGE="$REPO_ROOT/build/web-agent"
OUT_DIR="$REPO_ROOT/dist"
BIN_DIR="$REPO_ROOT/bin"

VSIX_ARTIFACT=""
VERSION="auto"
BUILD_DEB=false
BUILD_RPM=false
BUILD_BOTH=true
SKIP_EXTENSION=false
SKIP_WEB_AGENT=false
SKIP_TESTS=false
ARTIFACTS=()

info()    { echo "  [info]  $*" >&2; }
success() { echo "  [ ok ]  $*" >&2; }
warn()    { echo "  [warn]  $*" >&2; }
die()     { echo "  [error] $*" >&2; exit 1; }

usage() {
    awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
    exit 0
}

want_deb() { [[ "$BUILD_BOTH" == true || "$BUILD_DEB" == true ]]; }
want_rpm() { [[ "$BUILD_BOTH" == true || "$BUILD_RPM" == true ]]; }

normalize_version() {
    local raw="$1"
    raw="${raw#Cairo }"
    raw="${raw#cairo }"
    raw="${raw#v}"
    raw="${raw%% *}"
    raw="${raw//-/.}"
    raw="$(printf '%s' "$raw" | sed 's/[^A-Za-z0-9.+_~]/./g; s/^\.*//; s/\.*$//')"
    printf '%s\n' "${raw:-0.0.0}"
}

is_package_version() { [[ "$1" =~ ^[0-9][A-Za-z0-9.+_~]*$ ]]; }

detect_version() {
    local raw candidate

    # primary: git describe (matches what build.sh injects)
    raw="$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || true)"
    candidate="$(normalize_version "$raw")"
    if is_package_version "$candidate"; then
        printf '%s\n' "$candidate"; return
    fi

    # secondary: built cairo binary --version
    if [[ -x "$BIN_DIR/cairo" ]]; then
        raw="$("$BIN_DIR/cairo" --version 2>&1 || true)"
        candidate="$(normalize_version "$raw")"
        if is_package_version "$candidate"; then
            printf '%s\n' "$candidate"; return
        fi
    fi

    # tertiary: vscode-extension/package.json
    if [[ -f "$EXT_DIR/package.json" ]]; then
        raw="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$EXT_DIR/package.json" | head -1)"
        candidate="$(normalize_version "$raw")"
        if is_package_version "$candidate"; then
            printf '%s\n' "$candidate"; return
        fi
    fi

    printf '0.0.0\n'
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --version)
                shift; [[ $# -gt 0 ]] || die "--version requires a value"
                VERSION="$(normalize_version "$1")"
                ;;
            --version=*)
                VERSION="$(normalize_version "${1#*=}")"
                ;;
            --deb)  BUILD_RPM=false; BUILD_DEB=true;  BUILD_BOTH=false ;;
            --rpm)  BUILD_DEB=false; BUILD_RPM=true;  BUILD_BOTH=false ;;
            --all)  BUILD_DEB=false; BUILD_RPM=false; BUILD_BOTH=true  ;;
            --skip-extension) SKIP_EXTENSION=true ;;
            --skip-web-agent) SKIP_WEB_AGENT=true ;;
            --skip-tests)     SKIP_TESTS=true ;;
            --help|-h) usage ;;
            *) die "Unknown option: $1" ;;
        esac
        shift
    done
}

check_dependencies() {
    echo "" >&2
    echo "Checking dependencies..." >&2
    echo "------------------------" >&2

    local missing=()
    for cmd in bash git go sed; do
        command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
    done
    [[ ${#missing[@]} -eq 0 ]] || die "Missing required dependencies: ${missing[*]}"
    info "Go: $(go version 2>/dev/null | awk '{print $3}')"

    if [[ "$SKIP_EXTENSION" != true || "$SKIP_WEB_AGENT" != true ]]; then
        for cmd in node npm npx; do
            command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
        done
        [[ ${#missing[@]} -eq 0 ]] || die "Missing Node build dependencies: ${missing[*]}. Run: bash scripts/install-deps.sh --skip-cairo"
        info "Node: $(node --version)"
        info "npm:  $(npm --version)"
    fi

    local can_build=false
    if want_deb; then
        if command -v dpkg-deb >/dev/null 2>&1; then
            can_build=true
            info "dpkg-deb: available"
        elif [[ "$BUILD_DEB" == true ]]; then
            die "dpkg-deb is required for --deb. Install: sudo apt-get install -y dpkg fakeroot (Debian/Ubuntu) or sudo dnf install -y dpkg fakeroot (RHEL/Fedora)"
        else
            warn "dpkg-deb not found; skipping .deb. Install: sudo apt-get install -y dpkg fakeroot"
        fi
    fi

    if want_rpm; then
        if command -v rpmbuild >/dev/null 2>&1; then
            can_build=true
            info "rpmbuild: available"
        elif [[ "$BUILD_RPM" == true ]]; then
            die "rpmbuild is required for --rpm. Install: sudo dnf install -y rpm-build rpmdevtools (RHEL/Fedora)"
        else
            warn "rpmbuild not found; skipping .rpm. Install: sudo dnf install -y rpm-build rpmdevtools"
        fi
    fi

    [[ "$can_build" == true ]] || die "No package builder available. Install dpkg-deb or rpmbuild and retry."
    success "Dependencies satisfied"
}

run_pre_package_checks() {
    if [[ "$SKIP_TESTS" == true ]]; then
        info "Skipping pre-package checks (--skip-tests)"
        return
    fi
    bash "$SCRIPT_DIR/pre-package.sh"
}

clean_old_artifacts() {
    [[ -d "$OUT_DIR" ]] || return 0
    echo "" >&2
    echo "Clearing stale artifacts..." >&2
    echo "---------------------------" >&2
    rm -f "$OUT_DIR"/*.deb "$OUT_DIR"/*.rpm "$OUT_DIR"/*.vsix "$OUT_DIR"/SHA256SUMS 2>/dev/null || true
    info "Cleared $OUT_DIR"
}

build_binaries() {
    echo "" >&2
    echo "Building binaries..." >&2
    echo "--------------------" >&2
    mkdir -p "$OUT_DIR"
    bash "$REPO_ROOT/scripts/build.sh"
    for bin in cairo cairo-registry cairo-ctl; do
        [[ -x "$BIN_DIR/$bin" ]] || die "Build failed: $BIN_DIR/$bin not found"
        info "Binary: $BIN_DIR/$bin"
    done
    if [[ "$VERSION" == "auto" ]]; then
        VERSION="$(detect_version)"
    fi
    info "Package version: $VERSION"
}

build_extension() {
    if [[ "$SKIP_EXTENSION" == true ]]; then
        info "Skipping VS Code extension (--skip-extension)"
        return
    fi
    [[ -d "$EXT_DIR" ]] || die "Expected VS Code extension at $EXT_DIR"
    echo "" >&2
    echo "Building VS Code extension..." >&2
    echo "-----------------------------" >&2
    bash "$REPO_ROOT/scripts/build-extension.sh"
    VSIX_ARTIFACT="$(find "$EXT_DIR/.vscode-extension" -maxdepth 1 -type f -name '*.vsix' -print 2>/dev/null | sort -V | tail -1 || true)"
    [[ -n "$VSIX_ARTIFACT" && -f "$VSIX_ARTIFACT" ]] || die "Extension build did not produce a .vsix"
    info "VSIX: $VSIX_ARTIFACT"
}

build_web_agent() {
    if [[ "$SKIP_WEB_AGENT" == true ]]; then
        info "Skipping web agent (--skip-web-agent)"
        return
    fi
    [[ -d "$REPO_ROOT/web-agent" ]] || die "Expected web agent at $REPO_ROOT/web-agent"
    echo "" >&2
    echo "Building web agent..." >&2
    echo "---------------------" >&2
    bash "$REPO_ROOT/scripts/build-web-agent.sh"
    [[ -d "$WEB_AGENT_STAGE" ]] || die "Web agent build did not produce $WEB_AGENT_STAGE"
    info "Web agent: $WEB_AGENT_STAGE"
}

copy_standalone_vsix() {
    [[ -n "$VSIX_ARTIFACT" && -f "$VSIX_ARTIFACT" ]] || return 0
    local vsix_name
    vsix_name="$(basename "$VSIX_ARTIFACT")"
    install -m 0644 "$VSIX_ARTIFACT" "$OUT_DIR/$vsix_name"
    ARTIFACTS+=("$OUT_DIR/$vsix_name")
    info "Standalone VSIX: $OUT_DIR/$vsix_name"
}

# ---------------------------------------------------------------------------
# postinst / prerm helpers
# ---------------------------------------------------------------------------

write_cairo_postinst() {
    cat > "$1" <<'POSTINST_EOF'
#!/bin/sh
# Best-effort Cairo post-install hook.
set -u

VSIX_DIR=/usr/share/cairo
VSIX=$(ls -1 "$VSIX_DIR"/cairo-vscode-*.vsix 2>/dev/null | sort -V | tail -1 || true)

TARGET_USER="${SUDO_USER:-}"
[ -z "$TARGET_USER" ] && TARGET_USER=$(logname 2>/dev/null || true)

if [ -n "$TARGET_USER" ] && [ "$TARGET_USER" != "root" ]; then
    USER_UID=$(id -u "$TARGET_USER" 2>/dev/null || true)
    TARGET_HOME=$(getent passwd "$TARGET_USER" 2>/dev/null | cut -d: -f6 || true)
    [ -z "$TARGET_HOME" ] && TARGET_HOME="/home/$TARGET_USER"
    MARKER="$TARGET_HOME/.config/cairo/services.enabled"

    if [ -z "$USER_UID" ]; then
        echo "cairo: could not resolve uid for $TARGET_USER; skipping service auto-start."
        echo "       Run as your user: systemctl --user enable --now cairo.service cairo-web.service"
    else
        SYSTEMCTL_ENV="XDG_RUNTIME_DIR=/run/user/$USER_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$USER_UID/bus"

        loginctl enable-linger "$TARGET_USER" >/dev/null 2>&1 || true

        i=0
        while [ ! -S "/run/user/$USER_UID/bus" ] && [ "$i" -lt 5 ]; do
            sleep 1; i=$((i+1))
        done

        su -s /bin/sh "$TARGET_USER" -c "$SYSTEMCTL_ENV systemctl --user daemon-reload" >/dev/null 2>&1 || true

        if [ -e "$MARKER" ]; then
            : # services already initialized for this user; respect current state
        else
            echo "cairo: enabling cairo.service and cairo-web.service for $TARGET_USER"
            if su -s /bin/sh "$TARGET_USER" -c "$SYSTEMCTL_ENV systemctl --user enable --now cairo.service cairo-web.service"; then
                su -s /bin/sh "$TARGET_USER" -c "mkdir -p '$TARGET_HOME/.config/cairo' && touch '$MARKER'" >/dev/null 2>&1 || true
            else
                echo "cairo: could not enable services; run manually: systemctl --user enable --now cairo.service cairo-web.service"
            fi
        fi
    fi
else
    echo "cairo: no non-root target user found; skipping service auto-start."
    echo "       Run as your user: systemctl --user enable --now cairo.service cairo-web.service"
fi

# --- VS Code extension ---
if [ -z "$VSIX" ]; then
    echo "cairo: no bundled VSIX found in $VSIX_DIR; skipping VS Code registration."
    exit 0
fi

if [ -z "$TARGET_USER" ] || [ "$TARGET_USER" = "root" ]; then
    echo "cairo: no non-root target user found; skipping VS Code registration."
    echo "       Run manually as your user: code --install-extension $VSIX"
    exit 0
fi

CODE_CMD=""
for candidate in code code-insiders codium; do
    if su -s /bin/sh "$TARGET_USER" -c "command -v $candidate >/dev/null 2>&1"; then
        CODE_CMD="$candidate"; break
    fi
done

if [ -z "$CODE_CMD" ]; then
    echo "cairo: VS Code CLI not found for $TARGET_USER; skipping extension registration."
    echo "       Run manually as your user: code --install-extension $VSIX"
    exit 0
fi

echo "cairo: registering VS Code extension for $TARGET_USER with $CODE_CMD"
INSTALL_LOG=$(mktemp 2>/dev/null) || INSTALL_LOG="/tmp/cairo-vsix-install.$$"
INSTALL_RC=0
su -s /bin/sh "$TARGET_USER" -c "unset VSCODE_PORTABLE; $CODE_CMD --install-extension '$VSIX' --force" \
    >"$INSTALL_LOG" 2>&1 || INSTALL_RC=$?
grep -v "^mkdir: cannot create directory" "$INSTALL_LOG" || true
rm -f "$INSTALL_LOG"
[ "$INSTALL_RC" -ne 0 ] && echo "cairo: VS Code extension registration failed; run manually: code --install-extension $VSIX"

exit 0
POSTINST_EOF
    chmod 0755 "$1"
}

write_cairo_prerm() {
    cat > "$1" <<'PRERM_EOF'
#!/bin/sh
set -u

IS_UNINSTALL=0
if [ -n "${DPKG_MAINTSCRIPT_PACKAGE:-}" ]; then
    [ "${1:-}" = "remove" ] && IS_UNINSTALL=1
else
    [ "${1:-0}" = "0" ] && IS_UNINSTALL=1
fi

[ "$IS_UNINSTALL" = "1" ] || exit 0

TARGET_USER="${SUDO_USER:-}"
[ -z "$TARGET_USER" ] && TARGET_USER=$(logname 2>/dev/null || true)

if [ -n "$TARGET_USER" ] && [ "$TARGET_USER" != "root" ]; then
    TARGET_HOME=$(getent passwd "$TARGET_USER" 2>/dev/null | cut -d: -f6 || true)
    [ -z "$TARGET_HOME" ] && TARGET_HOME="/home/$TARGET_USER"
    USER_UID=$(id -u "$TARGET_USER" 2>/dev/null || true)
    if [ -n "$USER_UID" ]; then
        SYSTEMCTL_ENV="XDG_RUNTIME_DIR=/run/user/$USER_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$USER_UID/bus"
        su -s /bin/sh "$TARGET_USER" -c "$SYSTEMCTL_ENV systemctl --user disable --now cairo.service cairo-web.service" >/dev/null 2>&1 || true
    fi
    rm -f "$TARGET_HOME/.config/cairo/services.enabled" >/dev/null 2>&1 || true
fi

exit 0
PRERM_EOF
    chmod 0755 "$1"
}

write_registry_postinst() {
    cat > "$1" <<'POSTINST_EOF'
#!/bin/sh
set -u

# Create dedicated cairo user/group for the registry daemon.
if ! getent group cairo >/dev/null 2>&1; then
    groupadd -r cairo >/dev/null 2>&1 || true
fi
if ! getent passwd cairo >/dev/null 2>&1; then
    useradd -r -s /sbin/nologin -d /var/lib/cairo-registry -g cairo cairo >/dev/null 2>&1 || true
fi

# Belt-and-suspenders for pre-v235 systemd that ignores StateDirectory=.
mkdir -p /var/lib/cairo-registry
chown cairo:cairo /var/lib/cairo-registry 2>/dev/null || true

systemctl daemon-reload >/dev/null 2>&1 || true

if [ -n "${DPKG_MAINTSCRIPT_PACKAGE:-}" ]; then
    IS_INSTALL=false
    [ "${1:-}" = "configure" ] && IS_INSTALL=true
else
    IS_INSTALL=false
    [ "${1:-0}" = "1" ] && IS_INSTALL=true
fi

if [ "$IS_INSTALL" = "true" ]; then
    systemctl enable --now cairo-registry.service >/dev/null 2>&1 || \
        echo "cairo-registry: run manually: systemctl enable --now cairo-registry.service"
fi

exit 0
POSTINST_EOF
    chmod 0755 "$1"
}

write_registry_prerm() {
    cat > "$1" <<'PRERM_EOF'
#!/bin/sh
set -u

IS_UNINSTALL=0
if [ -n "${DPKG_MAINTSCRIPT_PACKAGE:-}" ]; then
    [ "${1:-}" = "remove" ] && IS_UNINSTALL=1
else
    [ "${1:-0}" = "0" ] && IS_UNINSTALL=1
fi

[ "$IS_UNINSTALL" = "1" ] || exit 0

systemctl disable --now cairo-registry.service >/dev/null 2>&1 || true

exit 0
PRERM_EOF
    chmod 0755 "$1"
}

# ---------------------------------------------------------------------------
# .deb builders
# ---------------------------------------------------------------------------

create_cairo_deb() {
    command -v dpkg-deb >/dev/null 2>&1 || { warn "dpkg-deb not found; skipping cairo .deb"; return 0; }

    echo "" >&2
    echo "Creating cairo .deb..." >&2
    echo "----------------------" >&2

    local pkg_dir arch deb_file depends
    pkg_dir="$(mktemp -d)"
    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    deb_file="$OUT_DIR/cairo_${VERSION}_${arch}.deb"
    depends="bash, ca-certificates, git, sqlite3"
    [[ "$SKIP_WEB_AGENT" != true ]] && depends="bash, ca-certificates, git, nodejs, sqlite3"

    mkdir -p "$pkg_dir/DEBIAN" "$pkg_dir/usr/local/bin" "$pkg_dir/usr/share/cairo" "$pkg_dir/usr/lib/systemd/user"

    install -m 0755 "$BIN_DIR/cairo" "$pkg_dir/usr/local/bin/cairo"

    [[ -n "$VSIX_ARTIFACT" ]] && install -m 0644 "$VSIX_ARTIFACT" "$pkg_dir/usr/share/cairo/$(basename "$VSIX_ARTIFACT")"

    if [[ "$SKIP_WEB_AGENT" != true ]]; then
        mkdir -p "$pkg_dir/usr/share/cairo/web-agent"
        cp -a "$WEB_AGENT_STAGE"/. "$pkg_dir/usr/share/cairo/web-agent/"
        install -m 0755 "$REPO_ROOT/scripts/cairo-web.sh" "$pkg_dir/usr/local/bin/cairo-web"
        cp "$SCRIPT_DIR/systemd/cairo-web.service" "$pkg_dir/usr/lib/systemd/user/cairo-web.service"
    fi
    cp "$SCRIPT_DIR/systemd/cairo.service" "$pkg_dir/usr/lib/systemd/user/cairo.service"

    cat > "$pkg_dir/DEBIAN/control" <<CONTROL_EOF
Package: cairo
Version: $VERSION
Architecture: $arch
Maintainer: selene@cairo.ai
Section: utils
Priority: optional
Depends: $depends
Recommends: code | code-insiders
Conflicts: cairo-agent
Replaces: cairo-agent
Description: Cairo AI coding harness with TUI, web agent, and VS Code extension
 Local-first AI coding harness with a keyboard-first TUI, a bundled
 VS Code extension, and a browser web agent. Installs to /usr/local/bin/cairo.
CONTROL_EOF

    write_cairo_postinst "$pkg_dir/DEBIAN/postinst"
    write_cairo_prerm    "$pkg_dir/DEBIAN/prerm"
    find "$pkg_dir" -type d -exec chmod 0755 {} +

    rm -f "$deb_file"
    if dpkg-deb --help 2>&1 | grep -q -- '--root-owner-group'; then
        dpkg-deb --root-owner-group --build "$pkg_dir" "$deb_file" >/dev/null
    elif command -v fakeroot >/dev/null 2>&1; then
        fakeroot dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    elif [[ $EUID -eq 0 ]]; then
        dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    else
        die "fakeroot or dpkg-deb --root-owner-group required to build cairo .deb"
    fi

    [[ -f "$deb_file" ]] || die "Failed to create $deb_file"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$deb_file")
    success "Created: $deb_file"
}

create_registry_deb() {
    command -v dpkg-deb >/dev/null 2>&1 || { warn "dpkg-deb not found; skipping cairo-registry .deb"; return 0; }

    echo "" >&2
    echo "Creating cairo-registry .deb..." >&2
    echo "--------------------------------" >&2

    local pkg_dir arch deb_file
    pkg_dir="$(mktemp -d)"
    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    deb_file="$OUT_DIR/cairo-registry_${VERSION}_${arch}.deb"

    mkdir -p "$pkg_dir/DEBIAN" "$pkg_dir/usr/local/bin" "$pkg_dir/usr/lib/systemd/system"

    install -m 0755 "$BIN_DIR/cairo-registry" "$pkg_dir/usr/local/bin/cairo-registry"
    cp "$SCRIPT_DIR/systemd/cairo-registry.service" "$pkg_dir/usr/lib/systemd/system/cairo-registry.service"

    cat > "$pkg_dir/DEBIAN/control" <<CONTROL_EOF
Package: cairo-registry
Version: $VERSION
Architecture: $arch
Maintainer: selene@cairo.ai
Section: utils
Priority: optional
Depends: bash, ca-certificates
Description: Cairo fleet registry daemon
 Persistent registry daemon that tracks cairo agent instances. Installs
 to /usr/local/bin/cairo-registry and runs as the cairo system user.
CONTROL_EOF

    write_registry_postinst "$pkg_dir/DEBIAN/postinst"
    write_registry_prerm    "$pkg_dir/DEBIAN/prerm"
    find "$pkg_dir" -type d -exec chmod 0755 {} +

    rm -f "$deb_file"
    if dpkg-deb --help 2>&1 | grep -q -- '--root-owner-group'; then
        dpkg-deb --root-owner-group --build "$pkg_dir" "$deb_file" >/dev/null
    elif command -v fakeroot >/dev/null 2>&1; then
        fakeroot dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    elif [[ $EUID -eq 0 ]]; then
        dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    else
        die "fakeroot or dpkg-deb --root-owner-group required to build cairo-registry .deb"
    fi

    [[ -f "$deb_file" ]] || die "Failed to create $deb_file"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$deb_file")
    success "Created: $deb_file"
}

create_ctl_deb() {
    command -v dpkg-deb >/dev/null 2>&1 || { warn "dpkg-deb not found; skipping cairo-ctl .deb"; return 0; }

    echo "" >&2
    echo "Creating cairo-ctl .deb..." >&2
    echo "--------------------------" >&2

    local pkg_dir arch deb_file
    pkg_dir="$(mktemp -d)"
    arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"
    deb_file="$OUT_DIR/cairo-ctl_${VERSION}_${arch}.deb"

    mkdir -p "$pkg_dir/DEBIAN" "$pkg_dir/usr/local/bin"

    install -m 0755 "$BIN_DIR/cairo-ctl" "$pkg_dir/usr/local/bin/cairo-ctl"

    cat > "$pkg_dir/DEBIAN/control" <<CONTROL_EOF
Package: cairo-ctl
Version: $VERSION
Architecture: $arch
Maintainer: selene@cairo.ai
Section: utils
Priority: optional
Depends: bash
Description: Cairo control CLI
 Control CLI for managing cairo instances and the fleet registry. Installs
 to /usr/local/bin/cairo-ctl.
CONTROL_EOF

    find "$pkg_dir" -type d -exec chmod 0755 {} +

    rm -f "$deb_file"
    if dpkg-deb --help 2>&1 | grep -q -- '--root-owner-group'; then
        dpkg-deb --root-owner-group --build "$pkg_dir" "$deb_file" >/dev/null
    elif command -v fakeroot >/dev/null 2>&1; then
        fakeroot dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    elif [[ $EUID -eq 0 ]]; then
        dpkg-deb --build "$pkg_dir" "$deb_file" >/dev/null
    else
        die "fakeroot or dpkg-deb --root-owner-group required to build cairo-ctl .deb"
    fi

    [[ -f "$deb_file" ]] || die "Failed to create $deb_file"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$deb_file")
    success "Created: $deb_file"
}

# ---------------------------------------------------------------------------
# .rpm builders
# ---------------------------------------------------------------------------

create_cairo_rpm() {
    command -v rpmbuild >/dev/null 2>&1 || { warn "rpmbuild not found; skipping cairo .rpm"; return 0; }

    echo "" >&2
    echo "Creating cairo .rpm..." >&2
    echo "----------------------" >&2

    local pkg_dir topdir spec_file rpm_file
    local vsix_install_line="" vsix_files_block="" web_agent_lines="" nodejs_req="" files_block
    pkg_dir="$(mktemp -d)"
    topdir="$pkg_dir/rpmbuild"
    spec_file="$pkg_dir/cairo.spec"
    mkdir -p "$topdir"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}

    files_block="%attr(0755,root,root) /usr/local/bin/cairo
%attr(0644,root,root) /usr/lib/systemd/user/cairo.service"

    if [[ -n "$VSIX_ARTIFACT" ]]; then
        local vsix_base
        vsix_base="$(basename "$VSIX_ARTIFACT")"
        vsix_install_line="install -D -m 0644 \"$VSIX_ARTIFACT\" \"%{buildroot}/usr/share/cairo/$vsix_base\""
        vsix_files_block="
%dir /usr/share/cairo
%attr(0644,root,root) /usr/share/cairo/$vsix_base"
    fi

    if [[ "$SKIP_WEB_AGENT" != true ]]; then
        cp "$SCRIPT_DIR/systemd/cairo-web.service" "$pkg_dir/cairo-web.service"
        web_agent_install_lines="install -D -m 0755 \"$REPO_ROOT/scripts/cairo-web.sh\" \"%{buildroot}/usr/local/bin/cairo-web\"
mkdir -p \"%{buildroot}/usr/share/cairo/web-agent\"
cp -a \"$WEB_AGENT_STAGE\"/. \"%{buildroot}/usr/share/cairo/web-agent/\"
install -D -m 0644 \"$pkg_dir/cairo-web.service\" \"%{buildroot}/usr/lib/systemd/user/cairo-web.service\""
        nodejs_req="Requires:       nodejs"
        files_block+="
%attr(0755,root,root) /usr/local/bin/cairo-web
/usr/share/cairo/web-agent
%attr(0644,root,root) /usr/lib/systemd/user/cairo-web.service"
    else
        web_agent_install_lines=""
    fi
    files_block+="${vsix_files_block}"

    cp "$SCRIPT_DIR/systemd/cairo.service" "$pkg_dir/cairo.service"
    write_cairo_postinst "$pkg_dir/postinst.sh"
    write_cairo_prerm    "$pkg_dir/prerm.sh"
    local postinst_body prerm_body
    postinst_body="$(sed '1{/^#!/d;}' "$pkg_dir/postinst.sh")"
    prerm_body="$(sed '1{/^#!/d;}' "$pkg_dir/prerm.sh")"

    cat > "$spec_file" <<SPEC_EOF
Name:           cairo
Version:        $VERSION
Release:        1%{?dist}
Summary:        Cairo AI coding harness with TUI, web agent, and VS Code extension
Packager:       selene@cairo.ai
License:        MIT
Requires:       bash
Requires:       ca-certificates
Requires:       git
$nodejs_req
Requires:       sqlite
Recommends:     code
Conflicts:      cairo-agent
Obsoletes:      cairo-agent

%description
Local-first AI coding harness with a keyboard-first TUI, a bundled
VS Code extension, and a browser web agent. Installs to /usr/local/bin/cairo.

%install
rm -rf "%{buildroot}"
install -D -m 0755 "$BIN_DIR/cairo" "%{buildroot}/usr/local/bin/cairo"
install -D -m 0644 "$pkg_dir/cairo.service" "%{buildroot}/usr/lib/systemd/user/cairo.service"
$vsix_install_line
$web_agent_install_lines

%post
$postinst_body

%preun
$prerm_body

%files
$files_block

%changelog
* $(date '+%a %b %d %Y') selene@cairo.ai - $VERSION-1
- Cairo AI coding harness with web agent and VS Code extension.
SPEC_EOF

    rpmbuild \
        --define "_topdir $topdir" \
        --define "_rpmdir $OUT_DIR" \
        -bb "$spec_file" >/dev/null

    rpm_file="$(find "$OUT_DIR" -type f -name "cairo-$VERSION-*.rpm" -print | sort -V | tail -1 || true)"
    [[ -n "$rpm_file" && -f "$rpm_file" ]] || die "Failed to locate built cairo RPM in $OUT_DIR"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$rpm_file")
    success "Created: $rpm_file"
}

create_registry_rpm() {
    command -v rpmbuild >/dev/null 2>&1 || { warn "rpmbuild not found; skipping cairo-registry .rpm"; return 0; }

    echo "" >&2
    echo "Creating cairo-registry .rpm..." >&2
    echo "--------------------------------" >&2

    local pkg_dir topdir spec_file rpm_file
    pkg_dir="$(mktemp -d)"
    topdir="$pkg_dir/rpmbuild"
    spec_file="$pkg_dir/cairo-registry.spec"
    mkdir -p "$topdir"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}

    cp "$SCRIPT_DIR/systemd/cairo-registry.service" "$pkg_dir/cairo-registry.service"
    write_registry_postinst "$pkg_dir/postinst.sh"
    write_registry_prerm    "$pkg_dir/prerm.sh"
    local postinst_body prerm_body
    postinst_body="$(sed '1{/^#!/d;}' "$pkg_dir/postinst.sh")"
    prerm_body="$(sed '1{/^#!/d;}' "$pkg_dir/prerm.sh")"

    cat > "$spec_file" <<SPEC_EOF
Name:           cairo-registry
Version:        $VERSION
Release:        1%{?dist}
Summary:        Cairo fleet registry daemon
Packager:       selene@cairo.ai
License:        MIT
Requires:       bash
Requires:       ca-certificates

%description
Persistent registry daemon that tracks cairo agent instances. Installs
to /usr/local/bin/cairo-registry and runs as the cairo system user.

%install
rm -rf "%{buildroot}"
install -D -m 0755 "$BIN_DIR/cairo-registry" "%{buildroot}/usr/local/bin/cairo-registry"
install -D -m 0644 "$pkg_dir/cairo-registry.service" "%{buildroot}/usr/lib/systemd/system/cairo-registry.service"

%post
$postinst_body

%preun
$prerm_body

%files
%attr(0755,root,root) /usr/local/bin/cairo-registry
%attr(0644,root,root) /usr/lib/systemd/system/cairo-registry.service

%changelog
* $(date '+%a %b %d %Y') selene@cairo.ai - $VERSION-1
- Cairo fleet registry daemon.
SPEC_EOF

    rpmbuild \
        --define "_topdir $topdir" \
        --define "_rpmdir $OUT_DIR" \
        -bb "$spec_file" >/dev/null

    rpm_file="$(find "$OUT_DIR" -type f -name "cairo-registry-$VERSION-*.rpm" -print | sort -V | tail -1 || true)"
    [[ -n "$rpm_file" && -f "$rpm_file" ]] || die "Failed to locate built cairo-registry RPM in $OUT_DIR"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$rpm_file")
    success "Created: $rpm_file"
}

create_ctl_rpm() {
    command -v rpmbuild >/dev/null 2>&1 || { warn "rpmbuild not found; skipping cairo-ctl .rpm"; return 0; }

    echo "" >&2
    echo "Creating cairo-ctl .rpm..." >&2
    echo "--------------------------" >&2

    local pkg_dir topdir spec_file rpm_file
    pkg_dir="$(mktemp -d)"
    topdir="$pkg_dir/rpmbuild"
    spec_file="$pkg_dir/cairo-ctl.spec"
    mkdir -p "$topdir"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}

    cat > "$spec_file" <<SPEC_EOF
Name:           cairo-ctl
Version:        $VERSION
Release:        1%{?dist}
Summary:        Cairo control CLI
Packager:       selene@cairo.ai
License:        MIT
Requires:       bash

%description
Control CLI for managing cairo instances and the fleet registry. Installs
to /usr/local/bin/cairo-ctl.

%install
rm -rf "%{buildroot}"
install -D -m 0755 "$BIN_DIR/cairo-ctl" "%{buildroot}/usr/local/bin/cairo-ctl"

%files
%attr(0755,root,root) /usr/local/bin/cairo-ctl

%changelog
* $(date '+%a %b %d %Y') selene@cairo.ai - $VERSION-1
- Cairo control CLI.
SPEC_EOF

    rpmbuild \
        --define "_topdir $topdir" \
        --define "_rpmdir $OUT_DIR" \
        -bb "$spec_file" >/dev/null

    rpm_file="$(find "$OUT_DIR" -type f -name "cairo-ctl-$VERSION-*.rpm" -print | sort -V | tail -1 || true)"
    [[ -n "$rpm_file" && -f "$rpm_file" ]] || die "Failed to locate built cairo-ctl RPM in $OUT_DIR"
    rm -rf "$pkg_dir"
    ARTIFACTS+=("$rpm_file")
    success "Created: $rpm_file"
}

# ---------------------------------------------------------------------------

write_checksums() {
    local checksum_file="$OUT_DIR/SHA256SUMS"
    : > "$checksum_file"
    for artifact in "${ARTIFACTS[@]}"; do
        (cd "$(dirname "$artifact")" && sha256sum "$(basename "$artifact")") >> "$checksum_file"
    done
    info "Checksums: $checksum_file"
}

print_success() {
    write_checksums
    echo ""
    echo "cairo2 package build complete"
    echo "-----------------------------"
    for artifact in "${ARTIFACTS[@]}"; do
        echo "  $artifact"
    done
    echo "  $OUT_DIR/SHA256SUMS"
}

main() {
    parse_args "$@"
    check_dependencies
    run_pre_package_checks
    clean_old_artifacts
    build_binaries
    build_extension
    build_web_agent
    copy_standalone_vsix

    if want_deb; then
        create_cairo_deb
        create_registry_deb
        create_ctl_deb
    fi
    if want_rpm; then
        create_cairo_rpm
        create_registry_rpm
        create_ctl_rpm
    fi

    [[ ${#ARTIFACTS[@]} -gt 0 ]] || die "No packages were built"
    print_success
}

main "$@"
