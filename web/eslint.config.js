import eslint from '@eslint/js'
import eslintConfigPrettier from 'eslint-config-prettier'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import reactHooks from 'eslint-plugin-react-hooks'
import simpleImportSort from 'eslint-plugin-simple-import-sort'
import globals from 'globals'
import tseslint from 'typescript-eslint'

// eslintConfig 定义 React 与 TypeScript 的类型感知静态检查
export default tseslint.config(
  {
    ignores: ['node_modules', '../internal/webui/dist'],
  },
  eslint.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  ...tseslint.configs.stylisticTypeChecked,
  reactHooks.configs.flat['recommended-latest'],
  jsxA11y.flatConfigs.strict,
  {
    languageOptions: {
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    plugins: {
      'simple-import-sort': simpleImportSort,
    },
    rules: {
      'simple-import-sort/imports': 'error',
      'simple-import-sort/exports': 'error',
      '@typescript-eslint/restrict-template-expressions': ['error', { allowNumber: true }],
      '@typescript-eslint/no-unused-vars': ['error', { ignoreRestSiblings: true }],
      'jsx-a11y/no-noninteractive-tabindex': ['error', { roles: ['log'], tags: [] }],
      'react-hooks/incompatible-library': 'off',
      'no-restricted-imports': [
        'error',
        { patterns: [{ group: ['../*', './../*'], message: '跨目录引用使用 @/ 别名' }] },
      ],
    },
  },
  {
    files: ['eslint.config.js', 'public/**/*.js'],
    extends: [tseslint.configs.disableTypeChecked],
  },
  eslintConfigPrettier,
)
