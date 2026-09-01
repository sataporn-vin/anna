import type {
  DashboardFilters,
  EnergyExperience,
  ExpenseSummary,
  FitnessPoint,
  FitnessTrends,
  Transaction,
} from "./types.js";

type ExtendedDate = { $date?: string };
type ExtendedObjectID = { $oid?: string };
type Document = Record<string, unknown>;

type AnnaEnvelope<T> = {
  data: T;
  meta?: { count?: number };
};

export class AnnaClient {
  constructor(
    private readonly baseUrl: string,
    private readonly token: string,
    private readonly timeoutMs: number,
  ) {}

  async getTransactions(filters: DashboardFilters, limit = 50): Promise<Transaction[]> {
    const documents = await this.find("transactions", transactionFilter(filters), {
      occurredOn: -1,
      createdAt: -1,
    }, limit);
    return documents.map(normalizeTransaction);
  }

  async getExpenseSummary(filters: DashboardFilters): Promise<ExpenseSummary> {
    const match = transactionFilter(filters);
    const [totalRows, categoryRows, dayRows] = await Promise.all([
      this.aggregate("transactions", [
        { $match: match },
        {
          $group: {
            _id: null,
            count: { $sum: 1 },
            totalMinor: { $sum: "$amount.minor" },
            currency: { $first: "$amount.currency" },
          },
        },
      ]),
      this.aggregate("transactions", [
        { $match: match },
        {
          $group: {
            _id: "$categoryPath",
            count: { $sum: 1 },
            totalMinor: { $sum: "$amount.minor" },
          },
        },
        { $sort: { totalMinor: -1 } },
      ]),
      this.aggregate("transactions", [
        { $match: match },
        {
          $group: {
            _id: "$occurredOn",
            count: { $sum: 1 },
            totalMinor: { $sum: "$amount.minor" },
          },
        },
        { $sort: { _id: 1 } },
      ]),
    ]);

    const total = totalRows[0] ?? {};
    return {
      count: numberValue(total.count),
      totalMinor: numberValue(total.totalMinor),
      currency: stringValue(total.currency) || "THB",
      byCategory: mergeCategoryRows(categoryRows),
      byDay: dayRows.map((row) => ({
        occurredOn: stringValue(row._id),
        totalMinor: numberValue(row.totalMinor),
        count: numberValue(row.count),
      })),
    };
  }

  async getExperiences(filters: DashboardFilters, limit = 100): Promise<EnergyExperience[]> {
    const documents = await this.find("experiences", dateFilter(filters), { occurredOn: -1, createdAt: -1 }, limit);
    return documents.map((document) => ({
      id: objectID(document._id),
      occurredOn: stringValue(document.occurredOn),
      summary: stringValue(document.summary),
      energyDelta: numberValue(document.energyDelta),
      context: stringArray(document.context),
    }));
  }

  async getFitnessTrends(filters: DashboardFilters, limit = 100): Promise<FitnessTrends> {
    const documents = await this.find("measurements", dateFilter(filters), { occurredOn: 1, createdAt: 1 }, limit);
    const points = documents.map(normalizeFitnessPoint).filter(hasFitnessSeries);
    const series = (["weight", "smm", "pbf", "totalBodyWater"] as const).filter((key) =>
      points.some((point) => point[key] !== undefined),
    );
    return { count: points.length, availableSeries: series, points };
  }

  private async find(
    collection: string,
    filter: Document,
    sort: Record<string, 1 | -1>,
    limit: number,
  ): Promise<Document[]> {
    const response = await this.request<AnnaEnvelope<Document[] | null>>("/v1/mongo/find", {
      collection,
      filter,
      sort,
      limit,
    });
    return response.data ?? [];
  }

  private async aggregate(collection: string, pipeline: Document[]): Promise<Document[]> {
    const response = await this.request<AnnaEnvelope<Document[] | null>>("/v1/mongo/aggregate", {
      collection,
      pipeline,
    });
    return response.data ?? [];
  }

  private async request<T>(path: string, body: unknown): Promise<T> {
    const response = await fetch(`${this.baseUrl}${path}`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${this.token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(this.timeoutMs),
    });
    if (!response.ok) {
      const text = await response.text();
      throw new Error(`Anna API ${path} returned ${response.status}: ${text.slice(0, 300)}`);
    }
    return (await response.json()) as T;
  }
}

function dateFilter(filters: DashboardFilters): Document {
  const occurredOn: Document = {};
  if (filters.occurredFrom) occurredOn.$gte = filters.occurredFrom;
  if (filters.occurredTo) occurredOn.$lte = filters.occurredTo;
  return Object.keys(occurredOn).length > 0 ? { occurredOn } : {};
}

function transactionFilter(filters: DashboardFilters): Document {
  const filter: Document = { transactionKind: "expense", ...dateFilter(filters) };
  if (filters.accountIds?.length) filter.accountId = { $in: filters.accountIds };
  if (filters.category) filter.categoryPath = filters.category;
  return filter;
}

function normalizeTransaction(document: Document): Transaction {
  const descriptor = document.descriptor as Document | undefined;
  const amount = document.amount as Document | undefined;
  return {
    id: objectID(document._id),
    occurredOn: stringValue(document.occurredOn),
    occurredAt: extendedDate(document.occurredAt),
    amountMinor: numberValue(amount?.minor),
    currency: stringValue(amount?.currency) || "THB",
    transactionKind: stringValue(document.transactionKind),
    accountId: stringValue(document.accountId),
    paymentChannelId: optionalString(document.paymentChannelId),
    descriptor: optionalString(descriptor?.raw),
    merchantName: optionalString(document.merchantName),
    categoryPath: stringArray(document.categoryPath),
    note: optionalString(document.note),
  };
}

function normalizeFitnessPoint(document: Document): FitnessPoint {
  const metrics = (document.metrics ?? document.values ?? {}) as Document;
  return {
    id: objectID(document._id),
    occurredOn: stringValue(document.occurredOn || document.measuredOn || document.date),
    weight: firstNumber(document, metrics, ["weight", "weightKg", "bodyWeightKg"]),
    smm: firstNumber(document, metrics, ["smm", "skeletalMuscleMass", "skeletalMuscleMassKg"]),
    pbf: firstNumber(document, metrics, ["pbf", "percentBodyFat", "bodyFatPercent"]),
    totalBodyWater: firstNumber(document, metrics, ["totalBodyWater", "totalBodyWaterL", "tbw"]),
  };
}

function firstNumber(document: Document, metrics: Document, keys: string[]): number | undefined {
  for (const key of keys) {
    const value = document[key] ?? metrics[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
  }
  return undefined;
}

function hasFitnessSeries(point: FitnessPoint): boolean {
  return Boolean(point.occurredOn) && [point.weight, point.smm, point.pbf, point.totalBodyWater].some((value) => value !== undefined);
}

function categoryLabel(value: unknown): string {
  const path = stringArray(value);
  return path[0] || "Uncategorized";
}

export function mergeCategoryRows(rows: Document[]): ExpenseSummary["byCategory"] {
  const categories = new Map<string, { totalMinor: number; count: number }>();
  for (const row of rows) {
    const category = categoryLabel(row._id);
    const current = categories.get(category) ?? { totalMinor: 0, count: 0 };
    current.totalMinor += numberValue(row.totalMinor);
    current.count += numberValue(row.count);
    categories.set(category, current);
  }
  return [...categories.entries()]
    .map(([category, values]) => ({ category, ...values }))
    .sort((left, right) => right.totalMinor - left.totalMinor || left.category.localeCompare(right.category));
}

function objectID(value: unknown): string {
  if (value && typeof value === "object") return stringValue((value as ExtendedObjectID).$oid);
  return stringValue(value);
}

function extendedDate(value: unknown): string | undefined {
  if (value && typeof value === "object") return optionalString((value as ExtendedDate).$date);
  return optionalString(value);
}

function numberValue(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function optionalString(value: unknown): string | undefined {
  const result = stringValue(value);
  return result || undefined;
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [];
}
