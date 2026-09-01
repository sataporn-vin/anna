import type { AnnaClient } from "./annaClient.js";
import type { DashboardData, DashboardFilters, EnergyExperience, EnergyTrends } from "./types.js";

export async function loadDashboard(
  client: AnnaClient,
  filters: DashboardFilters,
  transactionLimit = 50,
): Promise<DashboardData> {
  const [expenses, transactions, experiences, fitness] = await Promise.all([
    client.getExpenseSummary(filters),
    client.getTransactions(filters, transactionLimit),
    client.getExperiences(filters),
    client.getFitnessTrends(filters),
  ]);

  return {
    filters,
    expenses,
    transactions,
    energy: summarizeEnergy(experiences),
    fitness,
  };
}

export function summarizeEnergy(experiences: EnergyExperience[]): EnergyTrends {
  const dailyMap = new Map<string, { totalDelta: number; count: number }>();
  for (const experience of experiences) {
    const current = dailyMap.get(experience.occurredOn) ?? { totalDelta: 0, count: 0 };
    current.totalDelta += experience.energyDelta;
    current.count += 1;
    dailyMap.set(experience.occurredOn, current);
  }
  const totalDelta = experiences.reduce((total, experience) => total + experience.energyDelta, 0);
  const ranked = [...experiences].sort((left, right) => right.energyDelta - left.energyDelta);

  return {
    count: experiences.length,
    totalDelta,
    averageDelta: experiences.length ? totalDelta / experiences.length : 0,
    daily: [...dailyMap.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([occurredOn, value]) => ({ occurredOn, ...value })),
    topPositive: ranked.filter((experience) => experience.energyDelta > 0).slice(0, 5),
    topNegative: ranked.filter((experience) => experience.energyDelta < 0).reverse().slice(0, 5),
  };
}
