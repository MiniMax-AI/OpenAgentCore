import { createRoot } from "react-dom/client";

import { ConsoleAccess } from "./features/first-run/ConsoleAccess";
import { App } from "./App";
import { ToastProvider } from "./components/Toast";
import { ThemeProvider } from "./lib/ThemeProvider";
import "./i18n";
import "./style.css";
import "./styles/console.css";

const root = document.getElementById("root");

if (!root) throw new Error("Missing #root element.");

createRoot(root).render(
  <ThemeProvider>
    <ToastProvider>
      <ConsoleAccess><App /></ConsoleAccess>
    </ToastProvider>
  </ThemeProvider>,
);
