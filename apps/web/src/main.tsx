import { createRoot } from "react-dom/client";

import { ConsoleAccess } from "./features/first-run/ConsoleAccess";
import { App } from "./App";
import { ToastProvider } from "./components/Toast";
import { ThemeProvider } from "./lib/ThemeProvider";
import { LocaleProvider } from "./lib/LocaleProvider";
import "./style.css";

const root = document.getElementById("root");

if (!root) throw new Error("Missing #root element.");

createRoot(root).render(
  <ThemeProvider>
    <ToastProvider>
      <LocaleProvider><ConsoleAccess><App /></ConsoleAccess></LocaleProvider>
    </ToastProvider>
  </ThemeProvider>,
);
