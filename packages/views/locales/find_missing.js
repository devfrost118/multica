const fs = require('fs');
const path = require('path');

const enDir = path.join(__dirname, 'en');
const ruDir = path.join(__dirname, 'ru');

function getKeys(obj, prefix = '') {
  let keys = [];
  for (const key in obj) {
    const newPrefix = prefix ? `${prefix}.${key}` : key;
    if (typeof obj[key] === 'object' && obj[key] !== null && !Array.isArray(obj[key])) {
      keys = keys.concat(getKeys(obj[key], newPrefix));
    } else {
      keys.push(newPrefix);
    }
  }
  return keys;
}

const files = fs.readdirSync(enDir).filter(f => f.endsWith('.json'));

let missingCount = 0;

for (const file of files) {
  const enPath = path.join(enDir, file);
  const ruPath = path.join(ruDir, file);
  
  const enData = JSON.parse(fs.readFileSync(enPath, 'utf8'));
  const enKeys = getKeys(enData);
  
  if (fs.existsSync(ruPath)) {
    const ruData = JSON.parse(fs.readFileSync(ruPath, 'utf8'));
    const ruKeys = new Set(getKeys(ruData));
    
    const missing = enKeys.filter(k => !ruKeys.has(k));
    if (missing.length > 0) {
      console.log(`\nFile: ${file}`);
      missing.forEach(k => {
        console.log(`  Missing: ${k}`);
        missingCount++;
      });
    }
  } else {
    console.log(`\nFile missing entirely: ${file}`);
    missingCount += enKeys.length;
  }
}

if (missingCount === 0) {
  console.log('\nNo missing keys found.');
} else {
  console.log(`\nTotal missing keys: ${missingCount}`);
}
