import { PluginHost } from "./PluginHost";
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import * as Tooltip from "@radix-ui/react-tooltip";
import { OAuthCallback } from "./OAuthCredential";
import { App } from "./App";
import "./style.css";

const client = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
});
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={client}>
      <Tooltip.Provider>
        {location.pathname === "/oauth/callback" ? <OAuthCallback /> : <PluginHost><App /></PluginHost>}
      </Tooltip.Provider>
    </QueryClientProvider>
  </React.StrictMode>,
);
