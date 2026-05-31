"""Regression tests for v0.0.11 sync entry-point fix.

v0.0.10 bug (cc-main verified 2026-05-31): pyproject.toml `[project.scripts]`
pointed `eidetic-mcp = "eidetic_mcp.server:main"` but `main()` is `async def`.
The console-script wrapper does `sys.exit(main())` which discards the
unawaited coroutine, exits 0, and any MCP client gets "Failed to connect".

v0.0.11 fix: add `cli()` sync wrapper that wraps `asyncio.run(main())`;
pyproject.toml entry-point updated to point at `cli`.
"""
from __future__ import annotations

import inspect

import eidetic_mcp
import eidetic_mcp.server


def test_version_is_0_0_11() -> None:
    assert eidetic_mcp.__version__ == "0.0.11"


def test_main_is_async() -> None:
    """main() must remain async — production runtime hits stdio_server async ctx."""
    assert inspect.iscoroutinefunction(eidetic_mcp.server.main), \
        "main() must be async def — runtime requires async context for stdio_server"


def test_cli_exists_and_is_sync() -> None:
    """cli() must exist + be sync (callable by console-script wrapper)."""
    assert hasattr(eidetic_mcp.server, "cli"), \
        "cli() must exist as sync entry-point"
    assert not inspect.iscoroutinefunction(eidetic_mcp.server.cli), \
        "cli() must be sync (NOT async) — pyproject.toml [project.scripts] cannot await coroutines"


def test_cli_wraps_async_main_via_asyncio_run() -> None:
    """cli() body must call asyncio.run(main()) — the actual fix."""
    src = inspect.getsource(eidetic_mcp.server.cli)
    assert "asyncio.run" in src, f"cli() must use asyncio.run; got: {src!r}"
    assert "main()" in src, f"cli() must invoke main(); got: {src!r}"


def test_pyproject_entrypoint_points_at_cli() -> None:
    """Verify pyproject.toml [project.scripts] points at the sync wrapper, not async main."""
    from pathlib import Path
    pyproject = Path(__file__).resolve().parent.parent / "pyproject.toml"
    text = pyproject.read_text()
    assert 'eidetic-mcp = "eidetic_mcp.server:cli"' in text, \
        f"pyproject.toml must point at server:cli (sync wrapper) — got:\n{text}"
    assert 'eidetic-mcp = "eidetic_mcp.server:main"' not in text, \
        "pyproject.toml must NOT point at server:main (async) — that's the v0.0.10 bug"
