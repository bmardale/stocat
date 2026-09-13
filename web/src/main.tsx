import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { createAppRouter } from "./router";
import "./style.css";

const container = document.getElementById("app");

if (!container) {
  throw new Error("Missing application container");
}

const router = createAppRouter(new QueryClient());

createRoot(container).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
