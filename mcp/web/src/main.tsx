import { useCallback, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { useApp } from "@modelcontextprotocol/ext-apps/react";

import type { DashboardData, DashboardFilters, FitnessPoint, Transaction } from "../../src/types";
import "./styles.css";

type View = "expenses" | "energy" | "fitness";
type DashboardResult = { dashboard?: DashboardData };
type FitnessSeries = "weight" | "smm" | "pbf" | "totalBodyWater";

const CATEGORY_COLORS = ["#6c63ff", "#00a896", "#ff9f1c", "#e85d75", "#457b9d", "#8f5bd6"];
const SERIES: Array<{ key: FitnessSeries; label: string; color: string }> = [
  { key: "weight", label: "Weight", color: "#6c63ff" },
  { key: "smm", label: "SMM", color: "#00a896" },
  { key: "pbf", label: "PBF", color: "#e85d75" },
  { key: "totalBodyWater", label: "Body water", color: "#457b9d" },
];

function Dashboard() {
  const [data, setData] = useState<DashboardData | null>(null);
  const [view, setView] = useState<View>("expenses");
  const [filters, setFilters] = useState<DashboardFilters>({});
  const [accountText, setAccountText] = useState("");
  const [knownCategories, setKnownCategories] = useState<string[]>([]);
  const [expandedTransaction, setExpandedTransaction] = useState<string | null>(null);
  const [selectedSeries, setSelectedSeries] = useState<Set<FitnessSeries>>(new Set(SERIES.map(({ key }) => key)));
  const [loading, setLoading] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  const acceptResult = useCallback((result: DashboardResult | undefined) => {
    if (!result?.dashboard) return;
    setData(result.dashboard);
    setFilters(result.dashboard.filters);
    setAccountText(result.dashboard.filters.accountIds?.join(", ") ?? "");
    setKnownCategories((current) => [
      ...new Set([...current, ...result.dashboard!.expenses.byCategory.map(({ category }) => category)]),
    ]);
  }, []);

  const { app, isConnected, error } = useApp({
    appInfo: { name: "Anna Dashboard", version: "0.1.0" },
    capabilities: {},
    onAppCreated: (createdApp) => {
      createdApp.ontoolresult = (notification) => {
        acceptResult(notification.params.structuredContent as DashboardResult | undefined);
      };
    },
  });

  const refresh = async () => {
    if (!app) return;
    setLoading(true);
    setMessage(null);
    const nextFilters: DashboardFilters = {
      ...filters,
      accountIds: accountText
        .split(",")
        .map((value) => value.trim())
        .filter(Boolean),
    };
    if (!nextFilters.accountIds?.length) delete nextFilters.accountIds;
    try {
      const result = await app.callServerTool({ name: "get_dashboard_data", arguments: nextFilters });
      if (result.isError) {
        setMessage("The dashboard refresh failed.");
      } else {
        acceptResult(result.structuredContent as DashboardResult | undefined);
      }
    } catch (refreshError) {
      setMessage(refreshError instanceof Error ? refreshError.message : "The dashboard refresh failed.");
    } finally {
      setLoading(false);
    }
  };

  if (error) return <StateCard title="Dashboard unavailable" detail={error.message} />;
  if (!isConnected || !data) return <StateCard title="Loading Anna dashboard" detail="Connecting to the read-only MCP tools…" />;

  return (
    <main>
      <header className="hero">
        <div>
          <p className="eyebrow">ANNA · PERSONAL SIGNALS</p>
          <h1>Your life, in useful detail.</h1>
          <p className="subtitle">Spending, energy, and fitness without leaving the conversation.</p>
        </div>
        <span className="read-only">Read only</span>
      </header>

      <section className="filters" aria-label="Dashboard filters">
        <label>
          From
          <input
            type="date"
            value={filters.occurredFrom ?? ""}
            onChange={(event) => setFilters({ ...filters, occurredFrom: event.target.value || undefined })}
          />
        </label>
        <label>
          To
          <input
            type="date"
            value={filters.occurredTo ?? ""}
            onChange={(event) => setFilters({ ...filters, occurredTo: event.target.value || undefined })}
          />
        </label>
        <label>
          Accounts
          <input
            value={accountText}
            placeholder="kbank-saving, grab-credit-line"
            onChange={(event) => setAccountText(event.target.value)}
          />
        </label>
        <label>
          Category
          <select
            value={filters.category ?? ""}
            onChange={(event) => setFilters({ ...filters, category: event.target.value || undefined })}
          >
            <option value="">All categories</option>
            {knownCategories.map((category) => (
              <option value={category} key={category}>{category}</option>
            ))}
          </select>
        </label>
        <button type="button" onClick={refresh} disabled={loading}>{loading ? "Refreshing…" : "Apply"}</button>
      </section>
      {message && <p className="error-banner">{message}</p>}

      <nav className="tabs" aria-label="Dashboard views">
        {(["expenses", "energy", "fitness"] as View[]).map((item) => (
          <button
            type="button"
            key={item}
            className={view === item ? "active" : ""}
            aria-pressed={view === item}
            onClick={() => setView(item)}
          >
            {item === "energy" ? "Kokomi energy" : titleCase(item)}
          </button>
        ))}
      </nav>

      {view === "expenses" && (
        <ExpensesView
          data={data}
          expandedTransaction={expandedTransaction}
          onExpand={setExpandedTransaction}
        />
      )}
      {view === "energy" && <EnergyView data={data} />}
      {view === "fitness" && (
        <FitnessView data={data} selectedSeries={selectedSeries} onSeriesChange={setSelectedSeries} />
      )}
    </main>
  );
}

function ExpensesView({
  data,
  expandedTransaction,
  onExpand,
}: {
  data: DashboardData;
  expandedTransaction: string | null;
  onExpand: (id: string | null) => void;
}) {
  const { expenses, transactions } = data;
  return (
    <div className="view-grid">
      <section className="stats" aria-label="Expense totals">
        <Stat label="Total spent" value={formatMinor(expenses.totalMinor, expenses.currency)} />
        <Stat label="Transactions" value={String(expenses.count)} />
        <Stat
          label="Daily average"
          value={formatMinor(expenses.byDay.length ? Math.round(expenses.totalMinor / expenses.byDay.length) : 0, expenses.currency)}
        />
      </section>

      <section className="panel chart-panel">
        <PanelHeading title="Where it went" detail="Top-level category totals" />
        {expenses.byCategory.length ? (
          <ResponsiveContainer width="100%" height={230}>
            <BarChart data={expenses.byCategory} margin={{ left: 12, right: 12 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="category" tickLine={false} axisLine={false} />
              <YAxis tickFormatter={(value) => compactMoney(value, expenses.currency)} tickLine={false} axisLine={false} />
              <Tooltip formatter={(value) => formatMinor(Number(value), expenses.currency)} />
              <Bar dataKey="totalMinor" radius={[8, 8, 0, 0]}>
                {expenses.byCategory.map((entry, index) => <Cell key={entry.category} fill={CATEGORY_COLORS[index % CATEGORY_COLORS.length]} />)}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        ) : <EmptyState text="No expense data matches these filters." />}
      </section>

      <section className="panel chart-panel">
        <PanelHeading title="Daily pace" detail="Expense total by local date" />
        {expenses.byDay.length ? (
          <ResponsiveContainer width="100%" height={230}>
            <LineChart data={expenses.byDay} margin={{ left: 12, right: 18 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="occurredOn" tickFormatter={shortDate} tickLine={false} axisLine={false} />
              <YAxis tickFormatter={(value) => compactMoney(value, expenses.currency)} tickLine={false} axisLine={false} />
              <Tooltip labelFormatter={longDate} formatter={(value) => formatMinor(Number(value), expenses.currency)} />
              <Line type="monotone" dataKey="totalMinor" stroke="#6c63ff" strokeWidth={3} dot={{ r: 3 }} />
            </LineChart>
          </ResponsiveContainer>
        ) : <EmptyState text="No daily trend is available." />}
      </section>

      <section className="panel transaction-panel">
        <PanelHeading title="Transactions" detail={`${transactions.length} recent matching records`} />
        <div className="transaction-list">
          {transactions.map((transaction) => (
            <TransactionRow
              key={transaction.id}
              transaction={transaction}
              expanded={expandedTransaction === transaction.id}
              onExpand={() => onExpand(expandedTransaction === transaction.id ? null : transaction.id)}
            />
          ))}
          {!transactions.length && <EmptyState text="No transactions match these filters." />}
        </div>
      </section>
    </div>
  );
}

function EnergyView({ data }: { data: DashboardData }) {
  const energy = data.energy;
  return (
    <div className="view-grid">
      <section className="stats">
        <Stat label="Net energy" value={signed(energy.totalDelta)} tone={energy.totalDelta >= 0 ? "good" : "bad"} />
        <Stat label="Average" value={signed(energy.averageDelta)} />
        <Stat label="Experiences" value={String(energy.count)} />
      </section>
      <section className="panel chart-panel wide">
        <PanelHeading title="Energy timeline" detail="Daily sum of reported Kokomi energy changes" />
        {energy.daily.length ? (
          <ResponsiveContainer width="100%" height={250}>
            <BarChart data={energy.daily}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="occurredOn" tickFormatter={shortDate} tickLine={false} axisLine={false} />
              <YAxis tickLine={false} axisLine={false} />
              <Tooltip labelFormatter={longDate} />
              <Bar dataKey="totalDelta" radius={[6, 6, 0, 0]}>
                {energy.daily.map((entry) => <Cell key={entry.occurredOn} fill={entry.totalDelta >= 0 ? "#00a896" : "#e85d75"} />)}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        ) : <EmptyState text="No energy entries match these filters." />}
      </section>
      <ExperienceList title="Top positive" entries={energy.topPositive} tone="good" />
      <ExperienceList title="Top negative" entries={energy.topNegative} tone="bad" />
    </div>
  );
}

function FitnessView({
  data,
  selectedSeries,
  onSeriesChange,
}: {
  data: DashboardData;
  selectedSeries: Set<FitnessSeries>;
  onSeriesChange: (series: Set<FitnessSeries>) => void;
}) {
  const fitness = data.fitness;
  const toggle = (key: FitnessSeries) => {
    const next = new Set(selectedSeries);
    if (next.has(key)) next.delete(key); else next.add(key);
    onSeriesChange(next);
  };
  return (
    <div className="view-grid">
      <section className="panel wide">
        <PanelHeading title="Fitness and InBody" detail={`${fitness.count} measurement points`} />
        <div className="series-toggles">
          {SERIES.map((series) => (
            <label key={series.key} className={!fitness.availableSeries.includes(series.key) ? "disabled" : ""}>
              <input
                type="checkbox"
                checked={selectedSeries.has(series.key)}
                disabled={!fitness.availableSeries.includes(series.key)}
                onChange={() => toggle(series.key)}
              />
              <span style={{ background: series.color }} />
              {series.label}
            </label>
          ))}
        </div>
        {fitness.points.length ? (
          <ResponsiveContainer width="100%" height={300}>
            <LineChart data={fitness.points} margin={{ left: 8, right: 18 }}>
              <CartesianGrid strokeDasharray="3 3" vertical={false} />
              <XAxis dataKey="occurredOn" tickFormatter={shortDate} tickLine={false} axisLine={false} />
              <YAxis tickLine={false} axisLine={false} />
              <Tooltip labelFormatter={longDate} />
              <Legend />
              {SERIES.filter(({ key }) => selectedSeries.has(key)).map((series) => (
                <Line
                  key={series.key}
                  type="monotone"
                  dataKey={series.key}
                  name={series.label}
                  stroke={series.color}
                  strokeWidth={2.5}
                  connectNulls
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        ) : (
          <EmptyState text="No supported fitness measurements are stored yet. Add weight, SMM, PBF, or total body water records to see trends." />
        )}
      </section>
    </div>
  );
}

function TransactionRow({ transaction, expanded, onExpand }: { transaction: Transaction; expanded: boolean; onExpand: () => void }) {
  return (
    <button type="button" className="transaction" onClick={onExpand} aria-expanded={expanded}>
      <span className="transaction-date">{shortDate(transaction.occurredOn)}</span>
      <span className="transaction-main">
        <strong>{transaction.merchantName || transaction.descriptor || "Transaction"}</strong>
        <small>{transaction.categoryPath.join(" · ") || "Uncategorized"} · {transaction.accountId}</small>
        {expanded && (
          <span className="transaction-detail">
            {transaction.note || "No note"}
            {transaction.paymentChannelId ? ` · via ${transaction.paymentChannelId}` : ""}
          </span>
        )}
      </span>
      <strong className="transaction-amount">{formatMinor(transaction.amountMinor, transaction.currency)}</strong>
    </button>
  );
}

function ExperienceList({ title, entries, tone }: { title: string; entries: DashboardData["energy"]["topPositive"]; tone: "good" | "bad" }) {
  return (
    <section className="panel experience-panel">
      <PanelHeading title={title} detail="Strongest reported effects" />
      {entries.map((entry) => (
        <div className="experience" key={entry.id}>
          <span className={`delta ${tone}`}>{signed(entry.energyDelta)}</span>
          <div><strong>{entry.summary}</strong><small>{longDate(entry.occurredOn)} · {entry.context.join(" · ")}</small></div>
        </div>
      ))}
      {!entries.length && <EmptyState text={`No ${tone === "good" ? "positive" : "negative"} entries in this range.`} />}
    </section>
  );
}

function Stat({ label, value, tone }: { label: string; value: string; tone?: "good" | "bad" }) {
  return <div className={`stat ${tone ?? ""}`}><span>{label}</span><strong>{value}</strong></div>;
}

function PanelHeading({ title, detail }: { title: string; detail: string }) {
  return <div className="panel-heading"><h2>{title}</h2><p>{detail}</p></div>;
}

function EmptyState({ text }: { text: string }) {
  return <div className="empty">{text}</div>;
}

function StateCard({ title, detail }: { title: string; detail: string }) {
  return <main className="state"><div className="panel"><p className="eyebrow">ANNA DASHBOARD</p><h1>{title}</h1><p>{detail}</p></div></main>;
}

function formatMinor(minor: number, currency: string): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency, maximumFractionDigits: 2 }).format(minor / 100);
}

function compactMoney(minor: number, currency: string): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency, notation: "compact", maximumFractionDigits: 1 }).format(minor / 100);
}

function shortDate(date: string): string {
  if (!date) return "—";
  return new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(new Date(`${date}T00:00:00Z`));
}

function longDate(date: unknown): string {
  if (typeof date !== "string" || !date) return "Unknown date";
  return new Intl.DateTimeFormat("en-US", { year: "numeric", month: "short", day: "numeric" }).format(new Date(`${date}T00:00:00Z`));
}

function signed(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return rounded > 0 ? `+${rounded}` : String(rounded);
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

createRoot(document.getElementById("root")!).render(<Dashboard />);
