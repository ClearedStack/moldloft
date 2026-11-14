// Generated from the .json file of the same name. Do not edit.
globalThis.MOLDLOFT_EXAMPLES = [
  {
    "id": "leap_year_span",
    "label": "Leap-year February span: 2024-02-15 through 2024-03-15 at 166.50 a day",
    "inputs": {
      "daily_rate": "166.50",
      "end_date": "2024-03-15",
      "start_date": "2024-02-15"
    },
    "expect": {
      "billable_days": "30",
      "elapsed_days": "29",
      "period_allowance": "4995.00",
      "raw_inclusive_days": "30"
    }
  },
  {
    "id": "common_year_span",
    "label": "Common-year February span: 2025-02-15 through 2025-03-15 at 166.50 a day",
    "inputs": {
      "daily_rate": "166.50",
      "end_date": "2025-03-15",
      "start_date": "2025-02-15"
    },
    "expect": {
      "billable_days": "29",
      "elapsed_days": "28",
      "period_allowance": "4828.50",
      "raw_inclusive_days": "29"
    }
  },
  {
    "id": "single_day",
    "label": "Same start and end date: one inclusive billable day",
    "inputs": {
      "daily_rate": "95.25",
      "end_date": "2026-06-01",
      "start_date": "2026-06-01"
    },
    "expect": {
      "billable_days": "1",
      "elapsed_days": "0",
      "period_allowance": "95.25",
      "raw_inclusive_days": "1"
    }
  }
];
