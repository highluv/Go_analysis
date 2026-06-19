# internal/analyzer

Этап 2.3: вычисление allowed edges и evidence.

`analyzer` объединяет результаты:

- destination workload адресуем через `addressability`;
- source workload разрешён через `policy`;
- mTLS в MVP считается выполнимым всегда;
- каждое ребро получает evidence.

Результат привязывается к `analysis_run`, а не напрямую к snapshot. Один snapshot может использоваться многими независимыми запусками анализа.

Главные инварианты:

- оба workload ребра принадлежат snapshot запуска;
- `allowed_edge` не означает DENIED;
- у каждого ребра есть минимум одно evidence;
- вывод детерминированный, без зависимости от порядка обхода Go map.
