"""Server-side admin account management — the implementation behind the
`ezhik-lb admins` CLI command (see scripts/ezhik-lb). Deliberately talks to
the database directly instead of going through the HTTP API: it must keep
working even when the panel service itself is stopped, and it's the
designated recovery path for a locked-out admin.

Usage (normally invoked by the ezhik-lb wrapper script, which sources the
env file first so EZHIKLB_DATABASE_URL is set):

    python -m ezhiklb_panel.admin_cli list
    python -m ezhiklb_panel.admin_cli add <username>
    python -m ezhiklb_panel.admin_cli passwd <username>
    python -m ezhiklb_panel.admin_cli remove <username>
"""

from __future__ import annotations

import argparse
import asyncio
import getpass
import sys

from .config import load_settings
from .db import Database
from .security import hash_password
from .store import ConflictError, NotFoundError, Store


def _read_new_password() -> str:
    while True:
        password = getpass.getpass("Новый пароль (мин. 8 символов): ")
        if len(password) < 8:
            print("Пароль должен быть не короче 8 символов.", file=sys.stderr)
            continue
        confirm = getpass.getpass("Повторите пароль: ")
        if password != confirm:
            print("Пароли не совпадают, попробуйте снова.", file=sys.stderr)
            continue
        return password


async def _list(store: Store) -> int:
    admins = await store.list_admins()
    if not admins:
        print("Админов пока нет — первый будет создан при первом входе в панель.")
        return 0
    for admin in admins:
        print(f"{admin['username']}\tсоздан {admin['created_at']}\tобновлён {admin['updated_at']}")
    return 0


async def _add(store: Store, username: str) -> int:
    username = username.strip()
    if len(username) < 3:
        print("Логин должен быть не короче 3 символов.", file=sys.stderr)
        return 1
    password = _read_new_password()
    try:
        await store.create_admin_account(username, hash_password(password))
    except ConflictError as exc:
        print(f"Не удалось добавить: {exc}", file=sys.stderr)
        return 1
    print(f"Админ «{username}» добавлен.")
    return 0


async def _passwd(store: Store, username: str) -> int:
    username = username.strip()
    if await store.get_admin_by_username(username) is None:
        print(f"Админ «{username}» не найден.", file=sys.stderr)
        return 1
    password = _read_new_password()
    try:
        await store.update_admin_password(username, hash_password(password))
    except NotFoundError as exc:
        print(f"Не удалось изменить пароль: {exc}", file=sys.stderr)
        return 1
    print(f"Пароль «{username}» изменён.")
    return 0


async def _remove(store: Store, username: str) -> int:
    username = username.strip()
    confirm = input(f"Удалить админа «{username}»? Это необратимо. Введите «yes» для подтверждения: ")
    if confirm.strip().lower() != "yes":
        print("Отменено.")
        return 1
    try:
        await store.delete_admin(username)
    except (ConflictError, NotFoundError) as exc:
        print(f"Не удалось удалить: {exc}", file=sys.stderr)
        return 1
    print(f"Админ «{username}» удалён.")
    return 0


async def _run(args: argparse.Namespace) -> int:
    settings = load_settings()
    db = Database(settings.database_url)
    await db.migrate()
    store = Store(db)
    try:
        if args.command == "list":
            return await _list(store)
        if args.command == "add":
            return await _add(store, args.username)
        if args.command == "passwd":
            return await _passwd(store, args.username)
        if args.command == "remove":
            return await _remove(store, args.username)
        return 1
    finally:
        await db.close()


def main() -> None:
    parser = argparse.ArgumentParser(prog="ezhik-lb admins", description="Управление админами панели EzhikLB.")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("list", help="Список админов")
    add_parser = sub.add_parser("add", help="Добавить админа")
    add_parser.add_argument("username")
    passwd_parser = sub.add_parser("passwd", help="Изменить пароль админа")
    passwd_parser.add_argument("username")
    remove_parser = sub.add_parser("remove", help="Удалить админа")
    remove_parser.add_argument("username")

    args = parser.parse_args()
    sys.exit(asyncio.run(_run(args)))


if __name__ == "__main__":
    main()
