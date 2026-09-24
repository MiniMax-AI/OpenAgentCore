import { createRoot } from "react-dom/client";

import { ConsoleAccess } from "./features/first-run/ConsoleAccess";
import { ConsoleApp } from "./ConsoleApp";
import { ToastProvider } from "./components/Toast";
import { ThemeProvider } from "./lib/ThemeProvider";
import "./i18n";
import "./styles/app.css";

const root = document.getElementById("root");

if (!root) throw new Error("Missing #root element.");

createRoot(root).render(
  <ThemeProvider>
    <ToastProvider>
      <ConsoleAccess><ConsoleApp /></ConsoleAccess>
    </ToastProvider>
  </ThemeProvider>,
);
