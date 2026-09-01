FROM node:22-alpine AS build

WORKDIR /app
COPY mcp/package.json mcp/package-lock.json ./
RUN npm ci
COPY mcp ./
RUN npm run build

FROM node:22-alpine

ENV NODE_ENV=production
WORKDIR /app
COPY mcp/package.json mcp/package-lock.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY --from=build /app/dist ./dist

USER node
EXPOSE 3000
CMD ["node", "dist/server/server.js"]
