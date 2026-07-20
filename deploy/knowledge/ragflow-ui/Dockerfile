FROM node:20-bookworm-slim AS build

WORKDIR /workspace
COPY --from=ragflow-web package.json package-lock.json .npmrc ./
RUN npm ci

COPY --from=ragflow-web . ./
ENV VITE_BASE_URL=/ragflow/
ENV VITE_BUILD_SOURCEMAP=false
ENV VITE_MINIFY=esbuild
# Keep the one-time UI adapter build within an internal-trial Docker Desktop
# memory budget; it does not affect the RAGFlow runtime process.
ENV NODE_OPTIONS=--max-old-space-size=3072
RUN npm run build
