export type DashboardFilters = {
  occurredFrom?: string;
  occurredTo?: string;
  accountIds?: string[];
  category?: string;
};

export type Transaction = {
  id: string;
  occurredOn: string;
  occurredAt?: string;
  amountMinor: number;
  currency: string;
  transactionKind: string;
  accountId: string;
  paymentChannelId?: string;
  descriptor?: string;
  merchantName?: string;
  categoryPath: string[];
  note?: string;
};

export type ExpenseSummary = {
  count: number;
  totalMinor: number;
  currency: string;
  byCategory: Array<{ category: string; totalMinor: number; count: number }>;
  byDay: Array<{ occurredOn: string; totalMinor: number; count: number }>;
};

export type EnergyExperience = {
  id: string;
  occurredOn: string;
  summary: string;
  energyDelta: number;
  context: string[];
};

export type EnergyTrends = {
  count: number;
  totalDelta: number;
  averageDelta: number;
  daily: Array<{ occurredOn: string; totalDelta: number; count: number }>;
  topPositive: EnergyExperience[];
  topNegative: EnergyExperience[];
};

export type FitnessPoint = {
  id: string;
  occurredOn: string;
  weight?: number;
  smm?: number;
  pbf?: number;
  totalBodyWater?: number;
};

export type FitnessTrends = {
  count: number;
  availableSeries: Array<"weight" | "smm" | "pbf" | "totalBodyWater">;
  points: FitnessPoint[];
};

export type DashboardData = {
  filters: DashboardFilters;
  expenses: ExpenseSummary;
  transactions: Transaction[];
  energy: EnergyTrends;
  fitness: FitnessTrends;
};
