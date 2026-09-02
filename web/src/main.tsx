import { StrictMode } from "react"
import { createRoot } from "react-dom/client"

import { ThemeProvider } from "./app/providers/theme-provider"
import App from "./App"
import "./index.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider defaultTheme="dark">
      <App />
    </ThemeProvider>
  </StrictMode>,
)
