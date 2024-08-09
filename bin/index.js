#! /usr/bin/env node
import chalk from 'chalk';
import boxen from 'boxen';
import yargs from 'yargs';
import { hideBin } from 'yargs/helpers';
import fs from 'fs';
import path from 'path';
import dotenv from 'dotenv';
import yaml from 'js-yaml';
import axios from 'axios';
import ora from 'ora';
import cliCursor from 'cli-cursor';


// Load environment variables
dotenv.config();

  
const usage = chalk.blue("\nUsage: revyl <command> [options]\n"
  + boxen(chalk.green("\n" + "Revyl CLI Tool" + "\n"), {padding: 1, borderColor: 'green', dimBorder: true}) + "\n");

async function runTest(testConfig, speed, spinner) {
  const REVYL_API_KEY = process.env.REVYL_API_KEY;
  const url = process.env.REVYL_API_URL || 'https://device.cognisim.io/execute_test_id';

  try {
    const payload = {
      test_id: testConfig.id,
      speed: speed
    };

    // Only include optional fields if they exist in testConfig
    if (testConfig.get_downloads !== undefined) payload.get_downloads = testConfig.get_downloads;
    if (testConfig.local !== undefined) payload.local = testConfig.local;
    if (testConfig.backend_url) payload.backend_url = testConfig.backend_url;
    if (testConfig.action_url) payload.action_url = testConfig.action_url;
    if (testConfig.test_entrypoint) payload.test_entrypoint = testConfig.test_entrypoint;
    console.log(testConfig.local);
    spinner.text = `Running test ${testConfig.id}... (Sending request)`;
    const response = await axios.post(url, payload, {
      headers: {
        'Authorization': `Bearer ${REVYL_API_KEY}`,
        'Content-Type': 'application/json'
      }
    });

    spinner.text = `Running test ${testConfig.id}... (Processing response)`;
  
    if (response.data && response.data.success) {
      spinner.succeed(chalk.green(`Test ${testConfig.id} ran successfully`));
      return { testId: testConfig.id, success: true, result: response.data };
    } else {
      spinner.fail(chalk.red(`Test ${testConfig.id} failed`));
      console.log('Response data (failure):', JSON.stringify(response.data, null, 2));
      return { testId: testConfig.id, success: false, result: response.data };
    }
  } catch (error) {
    spinner.fail(chalk.red(`Error running test ${testConfig.id}: ${error.message}`));
    if (error.response) {
      console.log('Error response data:', JSON.stringify(error.response.data, null, 2));
      console.log('Error response status:', error.response.status);
      console.log('Error response headers:', JSON.stringify(error.response.headers, null, 2));
    } else if (error.request) {
      console.log('Error request:', error.request);
    } else {
      console.log('Error message:', error.message);
    }
    console.log('Error config:', JSON.stringify(error.config, null, 2));
    return { testId: testConfig.id, success: false, error: error.message };
  }
}

async function executeWorkflow(workflowData) {
  const speed = workflowData.speed || 2;
  const parallel = workflowData.parallel || false;

  console.log(chalk.cyan(`Executing workflow: ${workflowData.name} (v${workflowData.version})`));
  console.log(chalk.cyan(`Description: ${workflowData.description}`));
  console.log(chalk.cyan(`Speed: ${speed}`));
  console.log(chalk.cyan(`Parallel: ${parallel}`));

  console.log(chalk.yellow('\nRunning tests:'));

  try {
    const testResults = [];
    cliCursor.hide();

    if (parallel) {
      const spinners = workflowData.test_ids.map(testConfig => {
        const spinner = ora(`Running test ${testConfig.id}...`).start();
        spinner.indent = 2;
        return spinner;
      });

      const results = await Promise.all(workflowData.test_ids.map((testConfig, index) => 
        runTest(testConfig, speed, spinners[index])
      ));
      testResults.push(...results);
    } else {
      for (const testConfig of workflowData.test_ids) {
        const spinner = ora(`Running test ${testConfig.id}...`).start();
        spinner.indent = 2;
        const result = await runTest(testConfig, speed, spinner);
        testResults.push(result);
      }
    }

    cliCursor.show();

    console.log(chalk.yellow('\nTest Results Summary:'));
    testResults.forEach(result => {
      if (result.success) {
        console.log(chalk.green(`Test ${result.testId}: Passed`));
      } else {
        //console.log(chalk.red(`Test ${result.testId}: Failed`));
        if (result.error) {
          console.log(chalk.red(`  Error: ${result.error}`));
        }
      }
    });

    const allPassed = testResults.every(result => result.success);
    if (allPassed) {
      console.log(chalk.green('\nAll tests passed successfully!'));
    } else {
      console.log(chalk.red('\nSome tests failed. Please check the results above.'));
    }
  } catch (error) {
    console.error(chalk.red('Error executing workflow:', error.message));
  } finally {
    cliCursor.show();
  } 
}

yargs(hideBin(process.argv))
  .usage(usage)
  .command('run [file]', 'Run a YAML file from the .revyl folder', (yargs) => {
    return yargs
      .positional('file', {
        describe: 'YAML file to run',
        type: 'string'
      });
  }, async (argv) => {
    const REVYL_API_KEY = process.env.REVYL_API_KEY;

    if (!REVYL_API_KEY) {
      console.error(chalk.red('Error: REVYL_API_KEY not found in environment variables.'));
      process.exit(1);
    }

    const revylFolder = path.join(process.cwd(), '.revyl');
    
    if (!fs.existsSync(revylFolder)) {
      console.error(chalk.red('Error: .revyl folder not found in the current directory.'));
      process.exit(1);
    }

    const fileName = argv.file || 'index.yml';
    const filePath = path.join(revylFolder, fileName);

    if (!fs.existsSync(filePath)) {
      console.error(chalk.red(`Error: File ${filePath} not found.`));
      process.exit(1);
    }

    if (!filePath.endsWith('.yml') && !filePath.endsWith('.yaml')) {
      console.error(chalk.red('Error: File must be a YAML file (.yml or .yaml)'));
      process.exit(1);
    }

    console.log(chalk.green(`Running YAML file: ${filePath}`));
    try {
      const fileContents = fs.readFileSync(filePath, 'utf8');
      const data = yaml.load(fileContents);
      await executeWorkflow(data);
    } catch (err) {
      console.error(chalk.red('Error reading, parsing, or executing YAML file:'), err);
    }
  })
  .command('help', 'Show help', () => {}, (argv) => {
    console.log(chalk.yellow(boxen(
      chalk.bold('Revyl CLI Help\n\n') +
      chalk.green('Commands:\n') +
      '  run [file]  Run a YAML file from the .revyl folder\n' +
      '  help        Show this help message\n\n' +
      chalk.green('Options:\n') +
      '  --version   Show version number\n' +
      '  --help      Show help\n\n' +
      chalk.green('Examples:\n') +
      '  revyl run\n' +
      '  revyl run myfile.yml\n' +
      '  revyl help',
      {padding: 1, borderColor: 'yellow', dimBorder: true}
    )));
  })
  .demandCommand(1, 'You need to specify a command.')
  .help()
  .alias('h', 'help')
  .version('v', 'Show version number', '1.0.0')
  .alias('v', 'version')
  .argv;