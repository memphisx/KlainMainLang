<template>
  <article class="km-doc">
    <span class="km-eyebrow km-doc__eyebrow">Guides · Mobile</span>
    <h1>Cross-compile a CLI app for Sailfish OS</h1>
    <p class="km-doc__lede">
      Sailfish OS is glibc Linux (Jolla). Its shipping devices today are 64-bit ARM
      (<strong>aarch64</strong>), which is what this guide targets — though the OS has also run on
      32-bit ARM (<code>armv7hl</code>, e.g. the original Jolla phone, now stuck on a very old
      release) and on <strong>x86/x86_64</strong> (the Jolla Tablet, plus community ports to Intel
      netbooks and tablets). We support aarch64 for now; see the scope note. The ordinary native
      pipeline is at home there: you cross-compile a normal command-line program to a 64-bit-ARM
      binary, then package it as an
      installable RPM — including Sailfish's <strong>Harbour</strong> naming and private-library
      bundling. This guide is <strong>CLI-tool focused</strong>; see the scope note below for GUI
      and terminal-UI status. Every step here was verified building and running on a physical
      <strong>Xperia&nbsp;10&nbsp;II (Sailfish OS 5.1.0.11)</strong>.
    </p>

    <h2>What's supported today</h2>
    <ul>
      <li><strong>CLI programs</strong> — the whole runtime (fibers, <code>select()</code> loop,
        <code>fetch</code>/libcurl, RegExp/pcre2, crypto, <code>fs</code>, <code>process</code>)
        cross-compiles and runs.</li>
      <li><strong>RPM packaging</strong> — <code>-package=rpm</code> and
        <code>-package=rpm:harbour</code>, with non-allowlisted libraries bundled app-privately.</li>
    </ul>
    <p class="km-note">
      <strong>Scope.</strong> A native <em>webview/GUI</em> backend for Sailfish (its Gecko
      <code>sailfish-components-webview</code>) is planned but not yet implemented, so
      <code>klain:webview</code> apps don't target Sailfish yet. The <code>klain:tui</code>
      framework renders correctly on the device, and the shipped <code>klaintop</code> example runs
      on Sailfish once it reads process data portably (see "Write once, run everywhere" below).
      <strong>aarch64 first</strong>: it's what current devices ship, so that's where the initial
      support and on-device verification went. Sailfish also runs on 32-bit ARM
      (<code>armv7hl</code>, the earlier hardware) and x86/x86_64 (the Jolla Tablet and the community
      Intel ports); the <code>--target</code> machinery is generic, so those are a natural next step
      rather than a boundary — open an issue if you're building for one and it'll get prioritized.
    </p>

    <h2>1 · Where you build matters</h2>
    <p>
      Cross-compilation retargets both the C toolchain <em>and</em> the compiler's OS-specific
      codegen (the fiber scheduler's context layout, <code>process.platform</code>/<code>arch</code>,
      the <code>os</code> module, <code>fs</code> stat layouts, clocks), so the target OS is threaded
      through. Two build hosts work:
    </p>
    <ul>
      <li><strong>A Linux host</strong> (box, VM, or container) — the simplest: cross-<em>architecture</em>
        on the same OS (x86-64/arm64 <em>Linux</em> → aarch64 <em>Linux</em>), and <code>rpmbuild</code>
        for the packaging step is available. On an Apple-Silicon Mac, an <code>arm64</code> Linux
        container runs natively (no emulation), which makes a fine build host.</li>
      <li><strong>macOS → Linux</strong> directly: the compiled binary links on the Mac with
        <code>lld</code> on <code>PATH</code> (<code>brew install lld</code>) against the target
        <code>--sysroot</code>. Packaging (<code>-package=rpm</code>) still wants a host with
        <code>rpmbuild</code>, so the container route is handier when you also want the RPM.</li>
    </ul>
    <p>
      Other cross-OS pairs (a Windows target, or a macOS target from a non-macOS host) are rejected
      with a clear message, as is a <code>klain:webview</code>/<code>klain:tui</code> program built
      cross-OS (those C++ subsystems still build for the host).
    </p>

    <h2>2 · Get a Sailfish sysroot</h2>
    <p>
      Linking needs the target's own headers and libraries — a <em>sysroot</em>. The cheapest way to
      obtain a real one, without the full Jolla SDK, is to export the filesystem of the community
      builder image (exporting runs nothing, so no emulation):
    </p>
    <CodeBlock filename="get-sysroot.sh" :code="sysrootCode" />
    <p class="km-note">
      That tree carries Sailfish's real <code>/usr/lib64</code> layout, glibc, the gcc install, and
      headers. A device image ships only versioned <code>.so.N</code> runtime libraries; to link
      against a library you also need its <code>.so</code> developer symlink (a proper SDK
      <em>target</em> with the <code>-devel</code> packages has them, or add them yourself, e.g.
      <code>ln -s libpcre2-8.so.0 libpcre2-8.so</code>).
    </p>

    <h2>3 · Cross-compile the program</h2>
    <p>
      Write an ordinary CLI program — nothing Sailfish-specific:
    </p>
    <CodeBlock filename="greet.ts" :code="progCode" />
    <p>
      Compile it with the <code>sfos-aarch64</code> preset and point <code>--sysroot</code> at the
      exported tree:
    </p>
    <CodeBlock filename="build.sh" :code="buildCode" />
    <p>
      The preset resolves to Sailfish's native triple <code>aarch64-meego-linux-gnu</code>. That
      exact triple matters: it's how <code>clang</code> finds the target's <code>gcc</code>/crt
      objects and the RPM <code>/usr/lib64</code> layout in the sysroot — the generic
      <code>aarch64-linux-gnu</code> would fail to link. A genuine cross-arch link uses
      <code>lld</code> when it's on <code>PATH</code>.
    </p>

    <h2>4 · Package it as an RPM</h2>
    <p>
      <code>-package=rpm</code> emits a plain package; <code>-package=rpm:harbour</code> applies
      Sailfish Harbour naming (<code>harbour-&lt;name&gt;</code>). The <code>.spec</code> and a full
      <code>rpmbuild</code> tree are always written, and <code>rpmbuild</code> is run when it's on
      <code>PATH</code> — so a build host with <code>rpmbuild</code> produces the
      <code>.rpm</code> directly:
    </p>
    <CodeBlock filename="package.sh" :code="packageCode" />
    <p>
      Libraries not on the Harbour allowed-list — <code>pcre2</code> (used by RegExp), and
      <code>bdw-gc</code> when you build with <code>-mm=gc</code> — are bundled automatically: the
      SONAME is copied into <code>/usr/share/harbour-&lt;name&gt;/lib/</code> and the binary is
      linked with a matching <code>rpath</code>, the sanctioned Harbour pattern. <code>libcurl</code>,
      OpenSSL, <code>libstdc++</code>, <code>pthread</code>, and <code>libm</code> are
      Harbour-allowed and stay system libraries. (A bundled library must be present in the
      <code>--sysroot</code> with its <code>.so</code> dev symlink — e.g. <code>gc-devel</code> for
      <code>-mm=gc</code> — the same as pcre2.)
    </p>
    <p class="km-note">
      Metadata comes from <code>-app-name</code>, <code>-app-version</code>, and
      <code>-app-license</code> (the RPM <code>License:</code> tag, e.g. <code>GPLv3+</code>).
      A <code>-app-icon</code> <code>.png</code> adds a <code>.desktop</code> launcher + hicolor
      icon for a Harbour GUI app.
    </p>

    <h2>5 · Put it on the device</h2>
    <p>
      Copy the RPM to the phone and install it (installation needs root — <code>devel-su</code>),
      or copy the bare binary and run it directly:
    </p>
    <CodeBlock filename="deploy.sh" :code="deployCode" />

    <h2>Write once, run everywhere — without a runtime</h2>
    <p>
      The whole promise is one native binary that behaves correctly on each OS, with no interpreter
      or VM shipped alongside. The way you keep that promise in real apps is: <strong>detect where
      you are and reach for the right primitive</strong>. <code>process.platform</code> is a
      compile-time constant baked from the target, so the branch costs nothing at runtime.
    </p>
    <p>
      The shipped <code>klaintop</code> (a terminal process monitor) is the worked example. Its
      <code>klain:tui</code> UI — Yoga layout, the ANSI painter, Unicode box-drawing, and
      <code>klain:tty</code> raw-mode input — renders identically everywhere. The <em>data</em> is
      the interesting part: a process list has no single portable source. The naive version shelled
      out to <code>ps -axo …</code>, which works on macOS's BSD <code>ps</code> but fails on
      Sailfish, because Sailfish ships <strong>BusyBox</strong>, whose <code>ps</code> rejects those
      flags. The fix isn't to detect "am I on Sailfish?" — it's to prefer the portable
      <em>interface</em> over a platform-specific <em>tool</em>:
    </p>
    <CodeBlock filename="data.ts" :code="portableCode" />
    <p>
      <code>/proc</code> is the universal Linux interface — it reads the same on a glibc desktop, on
      Android/Termux, and on BusyBox-based Sailfish, none of which is true of a particular
      <code>ps</code> build. macOS has no <code>/proc</code>, so there the BSD <code>ps</code> branch
      is genuinely right. Same source, same binary shape, correct on each — the platform check is
      the only concession, and it's free.
    </p>
    <p class="km-note">
      Two runtime details make this "just work" once the branch is right: <code>fs.readFileSync</code>
      reads zero-length <code>/proc</code>/<code>/sys</code> pseudo-files correctly (they report size
      0 but stream real bytes), and a <code>klain:tui</code> app that sizes its root box from
      <code>process.stdout.columns</code>/<code>rows</code> (and repaints on <code>SIGWINCH</code>)
      fills whatever terminal it lands in — a phone's over SSH included. <code>klaintop</code> does
      both. raw mode is always restored on exit.
    </p>

    <h2>Verified on</h2>
    <p>
      Xperia&nbsp;10&nbsp;II, Sailfish OS 5.1.0.11 (aarch64, glibc&nbsp;2.41): a plain CLI binary, a
      RegExp binary resolving its bundled <code>libpcre2-8.so.0</code> from the Harbour lib path, a
      Harbour RPM read by the device's own <code>rpm</code>, and <code>klaintop</code> showing a
      live process table (reading Sailfish's <code>/proc</code>) that fills the terminal.
    </p>
  </article>
</template>

<script setup>
import CodeBlock from 'components/CodeBlock.vue'

const sysrootCode = `# Export a real aarch64 Sailfish rootfs to ./sfos-sysroot (needs Docker/Podman).
# Match the release to your device (here 5.1.0.11).
IMG=ghcr.io/sailfishos-open/docker-sailfishos-builder-aarch64:5.1.0.11
docker pull --platform=linux/arm64 "$IMG"
CID=$(docker create --platform=linux/arm64 "$IMG")
mkdir -p sfos-sysroot && docker export "$CID" | tar -C sfos-sysroot -xf -
docker rm "$CID"`

const progCode = `// greet.ts — an ordinary CLI program; nothing Sailfish-specific.
// In a compiled binary, argv[0] is the program and the first user arg is argv[1].
const who = process.argv[1] ?? "world";
console.log(\`Hello, \${who}!\`);
console.log(\`running on \${process.platform}/\${process.arch}\`);`

const buildCode = `# On a Linux host (or an arm64 Linux container on Apple Silicon).
klainmain --target sfos-aarch64 --sysroot ./sfos-sysroot -o greet greet.ts

# → ELF 64-bit aarch64, interpreter /lib/ld-linux-aarch64.so.1, for GNU/Linux`

const packageCode = `# Harbour RPM (rpmbuild present → builds harbour-greet-1.0.0-1.aarch64.rpm)
klainmain --target sfos-aarch64 --sysroot ./sfos-sysroot \\
  -package=rpm:harbour \\
  -app-name greet -app-version 1.0.0 -app-license BSD-3-Clause \\
  -o greet greet.ts

# Without rpmbuild on PATH, the .spec + build tree are written and the exact
# rpmbuild command to run elsewhere is printed.`

const portableCode = `// One binary, right behaviour per OS. process.platform is a compile-time
// constant, so this branch is free at runtime.
export function listProcs(): Proc[] {
  if (process.platform === "linux") return fromProc();  // glibc, Android, Sailfish…
  return fromPs();                                       // macOS BSD ps
}

// Linux: read /proc directly — the portable interface every Linux flavour shares,
// including BusyBox-based Sailfish (where shelling out to \`ps -axo\` would fail).
function fromProc(): Proc[] {
  const out: Proc[] = [];
  for (const name of readdirSync("/proc")) {
    const pid = parseInt(name, 10);
    if (!(pid > 0) || String(pid) !== name) continue;   // numeric pid dirs only
    try {
      const stat = readFileSync("/proc/" + pid + "/stat", "utf8").trim();
      const m = stat.match(/^\\d+ \\((.*)\\) (.+)$/);      // comm may contain spaces/)
      if (!m) continue;
      const f = m[2].split(/\\s+/);
      // …utime f[11] + stime f[12] for CPU%, VmRSS from /proc/<pid>/status for MEM%
      out.push({ pid, comm: m[1], /* … */ });
    } catch (e) { /* process vanished between readdir and read — skip */ }
  }
  return out;
}`

const deployCode = `# Install the RPM (as root on the device):
scp harbour-greet-1.0.0-1.aarch64.rpm defaultuser@phone:/home/defaultuser/
ssh defaultuser@phone 'devel-su rpm -i harbour-greet-1.0.0-1.aarch64.rpm'

# …or just run the bare binary:
scp greet defaultuser@phone:/home/defaultuser/ && ssh defaultuser@phone ./greet Kyriakos`
</script>
