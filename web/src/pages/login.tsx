import { Hexagon } from "lucide-react"
import { useEffect, useState } from "react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { api } from "@/lib/api"

export default function LoginPage({ onSuccess }: { onSuccess: () => Promise<void> }) {
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null)
  const [login, setLogin] = useState("")
  const [password, setPassword] = useState("")
  const [confirmPassword, setConfirmPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void api
      .setupRequired()
      .then((res) => setSetupRequired(res.setup_required))
      .catch(() => setSetupRequired(false))
  }, [])

  const isFirstRun = setupRequired === true
  const valid = login.trim().length >= 3 && password.length >= 8 && (!isFirstRun || password === confirmPassword)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!valid) return
    setBusy(true)
    try {
      await api.login(login.trim(), password)
      await onSuccess()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Ошибка входа")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-svh items-center justify-center overflow-hidden px-4">
      <div className="bg-primary/10 pointer-events-none absolute top-1/4 left-1/2 h-[480px] w-[680px] -translate-x-1/2 rounded-full blur-3xl" />
      <form onSubmit={submit} className="border-border bg-card relative z-10 flex w-full max-w-sm flex-col items-center gap-6 rounded-2xl border p-8 text-center shadow-lg">
        <div className="flex flex-col items-center gap-3">
          <div className="bg-accent flex h-10 w-10 items-center justify-center rounded-lg">
            <Hexagon className="h-5 w-5" />
          </div>
          <div>
            <div className="text-sm font-bold">EzhikLB</div>
            <div className="text-muted-foreground text-xs">control plane</div>
          </div>
        </div>
        <div>
          <p className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">Авторизация</p>
          <h1 className="mt-1 text-xl font-semibold">{isFirstRun ? "Создайте аккаунт администратора" : "Добро пожаловать"}</h1>
          <p className="text-muted-foreground mt-1 text-sm">{isFirstRun ? "Придумайте логин и пароль — это единственный вход в панель." : "Введите логин и пароль администратора."}</p>
        </div>
        <div className="flex w-full flex-col gap-3 text-left">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-login">Логин</Label>
            <Input
              id="admin-login"
              autoComplete="username"
              value={login}
              onChange={(e) => {
                setLogin(e.target.value)
                setError("")
              }}
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="admin-password">Пароль</Label>
            <Input
              id="admin-password"
              type="password"
              autoComplete={isFirstRun ? "new-password" : "current-password"}
              value={password}
              onChange={(e) => {
                setPassword(e.target.value)
                setError("")
              }}
            />
          </div>
          {isFirstRun && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="admin-password-confirm">Повторите пароль</Label>
              <Input
                id="admin-password-confirm"
                type="password"
                autoComplete="new-password"
                value={confirmPassword}
                onChange={(e) => {
                  setConfirmPassword(e.target.value)
                  setError("")
                }}
              />
              {confirmPassword && password !== confirmPassword && <span className="text-destructive text-xs">Пароли не совпадают</span>}
            </div>
          )}
          {error && <span className="text-destructive text-xs">{error}</span>}
        </div>
        <Button type="submit" className="w-full" disabled={busy || setupRequired === null || !valid}>
          {busy ? "Проверяю…" : isFirstRun ? "Создать аккаунт" : "Войти в панель"}
        </Button>
      </form>
    </div>
  )
}
